package tracer

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func (t *tracer) newRootSpan(name, service, resource string) *span {
	return t.StartSpan(name, SpanType("test"), ServiceName(service), ResourceName(resource)).(*span)
}

func (t *tracer) newChildSpan(name string, parent *span) *span {
	if parent == nil {
		return t.StartSpan(name).(*span)
	}
	return t.StartSpan(name, ChildOf(parent.Context())).(*span)
}

func TestTracerStart(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		Start()
		defer Stop()
		if _, ok := internal.GlobalTracer.(*tracer); !ok {
			t.Fail()
		}
	})

	t.Run("testing", func(t *testing.T) {
		internal.Testing = true
		Start()
		defer Stop()
		if _, ok := internal.GlobalTracer.(*tracer); ok {
			t.Fail()
		}
		if _, ok := internal.GlobalTracer.(*internal.NoopTracer); !ok {
			t.Fail()
		}
		internal.Testing = false
	})

	t.Run("deadlock/api", func(t *testing.T) {
		Stop()
		Stop()

		Start()
		Start()
		Start()

		// ensure at least one worker started and handles requests
		internal.GlobalTracer.(*tracer).forceFlush()

		Stop()
		Stop()
		Stop()
		Stop()
	})

	t.Run("deadlock/direct", func(t *testing.T) {
		tr, _ := getTestTracer()
		go tr.worker()
		tr.forceFlush() // blocks until worker is started
		select {
		case <-tr.stopped:
			t.Fatal("stopped channel should be open")
		default:
			// OK
		}
		tr.Stop()
		select {
		case <-tr.stopped:
			// OK
		default:
			t.Fatal("stopped channel should be closed")
		}
		tr.Stop()
		tr.Stop()
	})
}

func TestTracerStartSpan(t *testing.T) {
	tracer := newTracer()
	span := tracer.StartSpan("web.request").(*span)
	assert := assert.New(t)
	assert.NotEqual(uint64(0), span.TraceID)
	assert.NotEqual(uint64(0), span.SpanID)
	assert.Equal(uint64(0), span.ParentID)
	assert.Equal("web.request", span.Name)
	assert.Equal("tracer.test", span.Service)
	assert.NotNil(span.tracer)
}

func TestTracerStartSpanOptions(t *testing.T) {
	tracer := newTracer()
	now := time.Now()
	opts := []StartSpanOption{
		SpanType("test"),
		ServiceName("test.service"),
		ResourceName("test.resource"),
		StartTime(now),
	}
	span := tracer.StartSpan("web.request", opts...).(*span)
	assert := assert.New(t)
	assert.Equal("test", span.Type)
	assert.Equal("test.service", span.Service)
	assert.Equal("test.resource", span.Resource)
	assert.Equal(now.UnixNano(), span.Start)
}

func TestTracerStartChildSpan(t *testing.T) {
	t.Run("own-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		root := tracer.StartSpan("web.request", ServiceName("root-service")).(*span)
		child := tracer.StartSpan("db.query", ChildOf(root.Context()), ServiceName("child-service")).(*span)

		assert.NotEqual(uint64(0), child.TraceID)
		assert.NotEqual(uint64(0), child.SpanID)
		assert.Equal(root.SpanID, child.ParentID)
		assert.Equal(root.TraceID, child.ParentID)
		assert.Equal("child-service", child.Service)
	})

	t.Run("inherit-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		root := tracer.StartSpan("web.request", ServiceName("root-service")).(*span)
		child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)

		assert.Equal("root-service", child.Service)
	})
}

func TestTracerBaggagePropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	context := child.Context().(*spanContext)

	assert.Equal("value", context.baggage["key"])
}

func TestPropagationDefaults(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracer()
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("x", "y")
	ctx := root.Context().(*spanContext)
	headers := http.Header{}

	// inject the spanContext
	carrier := HTTPHeadersCarrier(headers)
	err := tracer.Inject(ctx, carrier)
	assert.Nil(err)

	tid := strconv.FormatUint(root.TraceID, 10)
	pid := strconv.FormatUint(root.SpanID, 10)

	// hardcode header names to fail test if defaults are changed
	assert.Equal(headers.Get(defaultTraceIDHeader), tid)
	assert.Equal(headers.Get(defaultParentIDHeader), pid)
	assert.Equal(headers.Get(defaultBaggageHeaderPrefix+"x"), "y")

	// retrieve the spanContext
	propagated, err := tracer.Extract(carrier)
	assert.Nil(err)
	pctx := propagated.(*spanContext)

	// compare if there is a Context match
	assert.Equal(ctx.traceID, pctx.traceID)
	assert.Equal(ctx.spanID, pctx.spanID)
	assert.Equal(ctx.baggage, pctx.baggage)

	// ensure a child can be created
	child := tracer.StartSpan("db.query", ChildOf(propagated)).(*span)

	assert.NotEqual(uint64(0), child.TraceID)
	assert.NotEqual(uint64(0), child.SpanID)
	assert.Equal(root.SpanID, child.ParentID)
	assert.Equal(root.TraceID, child.ParentID)
}

func TestTracerSamplingPriorityPropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	root := tracer.StartSpan("web.request", Tag(ext.SamplingPriority, 2)).(*span)
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	assert.Equal(float64(2), child.Metrics[samplingPriorityKey])
}

func TestTracerBaggageImmutability(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	child.SetBaggageItem("key", "changed!")
	parentContext := root.Context().(*spanContext)
	childContext := child.Context().(*spanContext)
	assert.Equal("value", parentContext.baggage["key"])
	assert.Equal("changed!", childContext.baggage["key"])
}

func TestTracerSpanTags(t *testing.T) {
	tracer := newTracer()
	tag := Tag("key", "value")
	span := tracer.StartSpan("web.request", tag).(*span)
	assert := assert.New(t)
	assert.Equal("value", span.Meta["key"])
}

func TestTracerSpanGlobalTags(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(WithGlobalTag("key", "value"))
	s := tracer.StartSpan("web.request").(*span)
	assert.Equal("value", s.Meta["key"])
	child := tracer.StartSpan("db.query", ChildOf(s.Context())).(*span)
	assert.Equal("value", child.Meta["key"])
}

func TestNewSpan(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create root spans
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	assert.Equal(uint64(0), span.ParentID)
	assert.Equal("pylons", span.Service)
	assert.Equal("pylons.request", span.Name)
	assert.Equal("/", span.Resource)
}

func TestNewSpanChild(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create child spans
	tracer := newTracer(withTransport(newDefaultTransport()))
	parent := tracer.newRootSpan("pylons.request", "pylons", "/")
	child := tracer.newChildSpan("redis.command", parent)
	// ids and services are inherited
	assert.Equal(parent.SpanID, child.ParentID)
	assert.Equal(parent.TraceID, child.TraceID)
	assert.Equal(parent.Service, child.Service)
	// the resource is not inherited and defaults to the name
	assert.Equal("redis.command", child.Resource)
	// the tracer instance is the same
	assert.Equal(tracer, parent.tracer)
	assert.Equal(tracer, child.tracer)
}

func TestNewRootSpanHasPid(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracer(withTransport(newDefaultTransport()))
	root := tracer.newRootSpan("pylons.request", "pylons", "/")

	assert.Equal(strconv.Itoa(os.Getpid()), root.Meta[ext.Pid])
}

func TestNewChildHasNoPid(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracer(withTransport(newDefaultTransport()))
	root := tracer.newRootSpan("pylons.request", "pylons", "/")
	child := tracer.newChildSpan("redis.command", root)

	assert.Equal("", child.Meta[ext.Pid])
}

func TestTracerSampler(t *testing.T) {
	assert := assert.New(t)

	sampler := NewRateSampler(0.9999) // high probability of sampling
	tracer := newTracer(
		withTransport(newDefaultTransport()),
		WithSampler(sampler),
	)

	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	if !sampler.Sample(span) {
		t.Skip("wasn't sampled") // no flaky tests
	}
	// only run test if span was sampled to avoid flaky tests
	_, ok := span.Metrics[sampleRateMetricKey]
	assert.True(ok)
}

func TestTracerEdgeSampler(t *testing.T) {
	assert := assert.New(t)

	// a sample rate of 0 should sample nothing
	tracer0 := newTracer(
		withTransport(newDefaultTransport()),
		WithSampler(NewRateSampler(0)),
	)
	// a sample rate of 1 should sample everything
	tracer1 := newTracer(
		withTransport(newDefaultTransport()),
		WithSampler(NewRateSampler(1)),
	)

	count := traceBufferSize / 3

	for i := 0; i < count; i++ {
		span0 := tracer0.newRootSpan("pylons.request", "pylons", "/")
		span0.Finish()
		span1 := tracer1.newRootSpan("pylons.request", "pylons", "/")
		span1.Finish()
	}

	assert.Len(tracer0.traceBuffer, 0)
	assert.Len(tracer1.traceBuffer, count)

	tracer0.Stop()
	tracer1.Stop()
}

func TestTracerConcurrent(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()
	defer tracer.Stop()

	// Wait for three different goroutines that should create
	// three different traces with one child each
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/home").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/trace").Finish()
	}()

	wg.Wait()
	tracer.forceFlush()
	traces := transport.Traces()
	assert.Len(traces, 3)
	assert.Len(traces[0], 1)
	assert.Len(traces[1], 1)
	assert.Len(traces[2], 1)
}

func TestTracerParentFinishBeforeChild(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()
	defer tracer.Stop()

	// Testing an edge case: a child refers to a parent that is already closed.

	parent := tracer.newRootSpan("pylons.request", "pylons", "/")
	parent.Finish()

	tracer.forceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 1)
	assert.Equal(parent, traces[0][0])

	child := tracer.newChildSpan("redis.command", parent)
	child.Finish()

	tracer.forceFlush()

	traces = transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 1)
	assert.Equal(child, traces[0][0])
	assert.Equal(parent.SpanID, traces[0][0].ParentID, "child should refer to parent, even if they have been flushed separately")
}

func TestTracerConcurrentMultipleSpans(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()
	defer tracer.Stop()

	// Wait for two different goroutines that should create
	// two traces with two children each
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		parent := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()
	go func() {
		defer wg.Done()
		parent := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()

	wg.Wait()
	tracer.forceFlush()
	traces := transport.Traces()
	assert.Len(traces, 2)
	assert.Len(traces[0], 2)
	assert.Len(traces[1], 2)
}

func TestTracerAtomicFlush(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()
	defer tracer.Stop()

	// Make sure we don't flush partial bits of traces
	root := tracer.newRootSpan("pylons.request", "pylons", "/")
	span := tracer.newChildSpan("redis.command", root)
	span1 := tracer.newChildSpan("redis.command.1", span)
	span2 := tracer.newChildSpan("redis.command.2", span)
	span.Finish()
	span1.Finish()
	span2.Finish()

	tracer.forceFlush()
	traces := transport.Traces()
	assert.Len(traces, 0, "nothing should be flushed now as span2 is not finished yet")

	root.Finish()

	tracer.forceFlush()
	traces = transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 4, "all spans should show up at once")
}

func TestTracerRace(t *testing.T) {
	assert := assert.New(t)

	tracer, transport := getTestTracer()
	defer tracer.Stop()

	total := (traceBufferSize / 3) / 10
	var wg sync.WaitGroup
	wg.Add(total)

	// Trying to be quite brutal here, firing lots of concurrent things, finishing in
	// different orders, and modifying spans after creation.
	for n := 0; n < total; n++ {
		i := n // keep local copy
		odd := ((i % 2) != 0)
		go func() {
			if i%11 == 0 {
				time.Sleep(time.Microsecond)
			}

			parent := tracer.newRootSpan("pylons.request", "pylons", "/")

			tracer.newChildSpan("redis.command", parent).Finish()
			child := tracer.newChildSpan("async.service", parent)

			if i%13 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				parent.SetTag("odd", "true")
				parent.SetTag("oddity", 1)
				parent.Finish()
			} else {
				child.SetTag("odd", "false")
				child.SetTag("oddity", 0)
				child.Finish()
			}

			if i%17 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				child.Resource = "HGETALL"
				child.SetTag("odd", "false")
				child.SetTag("oddity", 0)
			} else {
				parent.Resource = "/" + strconv.Itoa(i) + ".html"
				parent.SetTag("odd", "true")
				parent.SetTag("oddity", 1)
			}

			if i%19 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				child.Finish()
			} else {
				parent.Finish()
			}

			wg.Done()
		}()
	}

	wg.Wait()

	tracer.forceFlush()
	traces := transport.Traces()
	assert.Len(traces, total, "we should have exactly as many traces as expected")
	for _, trace := range traces {
		assert.Len(trace, 3, "each trace should have exactly 3 spans")
		var parent, child, redis *span
		for _, span := range trace {
			switch span.Name {
			case "pylons.request":
				parent = span
			case "async.service":
				child = span
			case "redis.command":
				redis = span
			default:
				assert.Fail("unexpected span", span)
			}
		}
		assert.NotNil(parent)
		assert.NotNil(child)
		assert.NotNil(redis)

		assert.Equal(uint64(0), parent.ParentID)
		assert.Equal(parent.TraceID, parent.SpanID)

		assert.Equal(parent.TraceID, redis.TraceID)
		assert.Equal(parent.TraceID, child.TraceID)

		assert.Equal(parent.TraceID, redis.ParentID)
		assert.Equal(parent.TraceID, child.ParentID)
	}
}

// TestWorker is definitely a flaky test, as here we test that the worker
// background task actually does flush things. Most other tests are and should
// be using forceFlush() to make sure things are really sent to transport.
// Here, we just wait until things show up, as we would do with a real program.
func TestWorker(t *testing.T) {
	assert := assert.New(t)

	tracer, transport := getTestTracer()
	defer tracer.Stop()

	n := traceBufferSize * 10 // put more traces than the chan size, on purpose
	for i := 0; i < n; i++ {
		root := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", root)
		child.Finish()
		root.Finish()
	}

	now := time.Now()
	count := 0
	for time.Now().Before(now.Add(time.Minute)) && count < traceBufferSize {
		nbTraces := len(transport.Traces())
		if nbTraces > 0 {
			t.Logf("popped %d traces", nbTraces)
		}
		count += nbTraces
		time.Sleep(time.Millisecond)
	}
	// here we just check that we have "enough traces". In practice, lots of them
	// are dropped, it's another interesting side-effect of this test: it does
	// trigger error messages (which are repeated, so it aggregates them etc.)
	if count < traceBufferSize {
		assert.Fail(fmt.Sprintf("timeout, not enough traces in buffer (%d/%d)", count, n))
	}
}

func newTracerChannels() *tracer {
	return &tracer{
		traceBuffer:    make(chan []*span, traceBufferSize),
		errorBuffer:    make(chan error, errorBufferSize),
		flushTracesReq: make(chan struct{}, 1),
		flushErrorsReq: make(chan struct{}, 1),
	}
}

func TestPushTrace(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracerChannels()
	trace := []*span{
		&span{
			Name:     "pylons.request",
			Service:  "pylons",
			Resource: "/",
		},
		&span{
			Name:     "pylons.request",
			Service:  "pylons",
			Resource: "/foo",
		},
	}
	tracer.pushTrace(trace)

	assert.Len(tracer.traceBuffer, 1, "there should be data in channel")
	assert.Len(tracer.flushTracesReq, 0, "no flush requested yet")

	pushed := <-tracer.traceBuffer
	assert.Equal(trace, pushed)

	many := traceBufferSize/2 + 1
	for i := 0; i < many; i++ {
		tracer.pushTrace(make([]*span, i))
	}
	assert.Len(tracer.traceBuffer, many, "all traces should be in the channel, not yet blocking")
	assert.Len(tracer.flushTracesReq, 1, "a trace flush should have been requested")

	for i := 0; i < cap(tracer.traceBuffer); i++ {
		tracer.pushTrace(make([]*span, i))
	}
	assert.Len(tracer.traceBuffer, traceBufferSize, "buffer should be full")
	assert.NotEqual(0, len(tracer.errorBuffer), "there should be an error logged")
	err := <-tracer.errorBuffer
	assert.Equal(&errBufferFull{name: "trace channel", size: traceBufferSize}, err)
}

func TestPushErr(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracerChannels()

	err := fmt.Errorf("ooops")
	tracer.pushErr(err)

	assert.Len(tracer.errorBuffer, 1, "there should be data in channel")
	assert.Len(tracer.flushErrorsReq, 0, "no flush requested yet")

	pushed := <-tracer.errorBuffer
	assert.Equal(err, pushed)

	many := errorBufferSize/2 + 1
	for i := 0; i < many; i++ {
		tracer.pushErr(fmt.Errorf("err %d", i))
	}
	assert.Len(tracer.errorBuffer, many, "all errs should be in the channel, not yet blocking")
	assert.Len(tracer.flushErrorsReq, 1, "a err flush should have been requested")
	for i := 0; i < cap(tracer.errorBuffer); i++ {
		tracer.pushErr(fmt.Errorf("err %d", i))
	}
	// if we reach this, means pushErr is not blocking, which is what we want to double-check
}

// BenchmarkConcurrentTracing tests the performance of spawning a lot of
// goroutines where each one creates a trace with a parent and a child.
func BenchmarkConcurrentTracing(b *testing.B) {
	tracer, _ := getTestTracer(WithSampler(NewRateSampler(0)))
	defer tracer.Stop()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		go func() {
			parent := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
			defer parent.Finish()

			for i := 0; i < 10; i++ {
				tracer.StartSpan("redis.command", ChildOf(parent.Context())).Finish()
			}
		}()
	}
}

// BenchmarkTracerAddSpans tests the performance of creating and finishing a root
// span. It should include the encoding overhead.
func BenchmarkTracerAddSpans(b *testing.B) {
	tracer, _ := getTestTracer(WithSampler(NewRateSampler(0)))
	defer tracer.Stop()

	for n := 0; n < b.N; n++ {
		span := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
		span.Finish()
	}
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer(opts ...StartOption) (*tracer, *dummyTransport) {
	transport := &dummyTransport{encoding: encodingMsgpack}
	o := append([]StartOption{withTransport(transport)}, opts...)
	tracer := newTracer(o...)
	return tracer, transport
}

// Mock Transport with a real Encoder
type dummyTransport struct {
	encoding
	traces [][]*span

	sync.RWMutex // required because of some poll-testing (eg: worker)
}

func (t *dummyTransport) sendTraces(traces [][]*span) (*http.Response, error) {
	t.Lock()
	t.traces = append(t.traces, traces...)
	t.Unlock()
	r, err := encode(t.encoding, traces)
	if err != nil {
		return nil, err
	}
	_, err = ioutil.ReadAll(r)
	return nil, err
}

func (t *dummyTransport) Traces() [][]*span {
	t.Lock()
	defer t.Unlock()

	traces := t.traces
	t.traces = nil
	return traces
}
