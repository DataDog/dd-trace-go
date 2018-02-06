package tracer

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/opentracing/opentracing-go"

	"github.com/stretchr/testify/assert"
)

var DefaultTracer = New(WithTransport(newDefaultTransport()))

func newChildSpan(name string, parent *span) *span {
	return DefaultTracer.newChildSpan(name, parent)
}

func TestOpenTracerStartSpan(t *testing.T) {
	tracer := New()
	span, ok := tracer.StartSpan("web.request").(*span)
	assert := assert.New(t)
	assert.True(ok)
	assert.NotEqual(uint64(0), span.TraceID)
	assert.NotEqual(uint64(0), span.SpanID)
	assert.Equal(uint64(0), span.ParentID)
	assert.Equal("web.request", span.Name)
	assert.Equal("tracer.test", span.Service)
	assert.NotNil(span.Tracer())
}

func TestOpenTracerStartChildSpan(t *testing.T) {
	assert := assert.New(t)
	tracer := New()
	root := tracer.StartSpan("web.request")
	child := tracer.StartSpan("db.query", opentracing.ChildOf(root.Context()))
	tRoot, ok := root.(*span)
	assert.True(ok)
	tChild, ok := child.(*span)
	assert.True(ok)

	assert.NotEqual(uint64(0), tChild.TraceID)
	assert.NotEqual(uint64(0), tChild.SpanID)
	assert.Equal(tRoot.SpanID, tChild.ParentID)
	assert.Equal(tRoot.TraceID, tChild.ParentID)
}

func TestOpenTracerBaggagePropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := New()
	root := tracer.StartSpan("web.request")
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", opentracing.ChildOf(root.Context()))
	context, ok := child.Context().(*spanContext)
	assert.True(ok)

	assert.Equal("value", context.baggage["key"])
}

func TestOpenTracerBaggageImmutability(t *testing.T) {
	assert := assert.New(t)
	tracer := New()
	root := tracer.StartSpan("web.request")
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", opentracing.ChildOf(root.Context()))
	child.SetBaggageItem("key", "changed!")
	parentContext, ok := root.Context().(*spanContext)
	assert.True(ok)
	childContext, ok := child.Context().(*spanContext)
	assert.True(ok)
	assert.Equal("value", parentContext.baggage["key"])
	assert.Equal("changed!", childContext.baggage["key"])
}

func TestOpenTracerSpanTags(t *testing.T) {
	tracer := New()
	tag := opentracing.Tag{Key: "key", Value: "value"}
	span, ok := tracer.StartSpan("web.request", tag).(*span)
	assert := assert.New(t)
	assert.True(ok)
	assert.Equal("value", span.Meta["key"])
}

func TestOpenTracerSpanGlobalTags(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithGlobalTags("key", "value"))
	s := tracer.StartSpan("web.request").(*span)
	assert.Equal("value", s.Meta["key"])
	child := tracer.StartSpan("db.query", opentracing.ChildOf(s.Context())).(*span)
	assert.Equal("value", child.Meta["key"])
}

func TestOpenTracerSpanStartTime(t *testing.T) {
	assert := assert.New(t)

	tracer := New()
	startTime := time.Now().Add(-10 * time.Second)
	span, ok := tracer.StartSpan("web.request", opentracing.StartTime(startTime)).(*span)
	assert.True(ok)
	assert.Equal(startTime.UnixNano(), span.Start)
}

func TestNewSpan(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create root spans
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	assert.Equal(uint64(0), span.ParentID)
	assert.Equal("pylons", span.Service)
	assert.Equal("pylons.request", span.Name)
	assert.Equal("/", span.Resource)
}

func TestNewSpanChild(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create child spans
	tracer := New(WithTransport(newDefaultTransport()))
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

func TestnewRootSpanHasPid(t *testing.T) {
	assert := assert.New(t)

	tracer := New(WithTransport(newDefaultTransport()))
	root := tracer.newRootSpan("pylons.request", "pylons", "/")

	assert.Equal(strconv.Itoa(os.Getpid()), root.getMeta(ext.Pid))
}

func TestNewChildHasNoPid(t *testing.T) {
	assert := assert.New(t)

	tracer := New(WithTransport(newDefaultTransport()))
	root := tracer.newRootSpan("pylons.request", "pylons", "/")
	child := tracer.newChildSpan("redis.command", root)

	assert.Equal("", child.getMeta(ext.Pid))
}

func TestTracerSampler(t *testing.T) {
	assert := assert.New(t)

	sampler := NewRateSampler(0.5)
	tracer := New(
		WithTransport(newDefaultTransport()),
		WithSampler(sampler),
	)

	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// The span might be sampled or not, we don't know, but at least it should have the sample rate metric
	assert.Equal(float64(0.5), span.Metrics[sampleRateMetricKey])
}

func TestTracerEdgeSampler(t *testing.T) {
	assert := assert.New(t)

	// a sample rate of 0 should sample nothing
	tracer0 := New(
		WithTransport(newDefaultTransport()),
		WithSampler(NewRateSampler(0)),
	)
	// a sample rate of 1 should sample everything
	tracer1 := New(
		WithTransport(newDefaultTransport()),
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
	tracer.ForceFlush()
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

	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 1)
	assert.Equal(parent, traces[0][0])

	child := tracer.newChildSpan("redis.command", parent)
	child.Finish()

	tracer.ForceFlush()

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
	tracer.ForceFlush()
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

	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 0, "nothing should be flushed now as span2 is not finished yet")

	root.Finish()

	tracer.ForceFlush()
	traces = transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 4, "all spans should show up at once")
}

func TestTracerServices(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()

	tracer.setServiceInfo("svc1", "a", "b")
	tracer.setServiceInfo("svc2", "c", "d")
	tracer.setServiceInfo("svc1", "e", "f")

	tracer.Stop()

	assert.Len(transport.services, 2)

	svc1 := transport.services["svc1"]
	assert.NotNil(svc1)
	assert.Equal("svc1", svc1.Name)
	assert.Equal("e", svc1.App)
	assert.Equal("f", svc1.AppType)

	svc2 := transport.services["svc2"]
	assert.NotNil(svc2)
	assert.Equal("svc2", svc2.Name)
	assert.Equal("c", svc2.App)
	assert.Equal("d", svc2.AppType)
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

			newChildSpan("redis.command", parent).Finish()
			child := newChildSpan("async.service", parent)

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

	tracer.ForceFlush()
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
// be using ForceFlush() to make sure things are really sent to transport.
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

func newTracerChannels() *Tracer {
	return &Tracer{
		traceBuffer:      make(chan []*span, traceBufferSize),
		serviceBuffer:    make(chan service, serviceBufferSize),
		errorBuffer:      make(chan error, errorBufferSize),
		flushTracesReq:   make(chan struct{}, 1),
		flushServicesReq: make(chan struct{}, 1),
		flushErrorsReq:   make(chan struct{}, 1),
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
	assert.Equal(&errorTraceChanFull{Len: traceBufferSize}, err)
}

func TestPushService(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracerChannels()

	svc := service{
		Name:    "redis-master",
		App:     "redis",
		AppType: "db",
	}
	tracer.pushService(svc)

	assert.Len(tracer.serviceBuffer, 1, "there should be data in channel")
	assert.Len(tracer.flushServicesReq, 0, "no flush requested yet")

	pushed := <-tracer.serviceBuffer
	assert.Equal(svc, pushed)

	many := serviceBufferSize/2 + 1
	for i := 0; i < many; i++ {
		tracer.pushService(service{
			Name:    fmt.Sprintf("service%d", i),
			App:     "custom",
			AppType: "web",
		})
	}
	assert.Len(tracer.serviceBuffer, many, "all services should be in the channel, not yet blocking")
	assert.Len(tracer.flushServicesReq, 1, "a service flush should have been requested")

	for i := 0; i < cap(tracer.serviceBuffer); i++ {
		tracer.pushService(service{
			Name:    fmt.Sprintf("service%d", i),
			App:     "custom",
			AppType: "web",
		})
	}
	assert.Len(tracer.serviceBuffer, serviceBufferSize, "buffer should be full")
	assert.NotEqual(0, len(tracer.errorBuffer), "there should be an error logged")
	err := <-tracer.errorBuffer
	assert.Equal(&errorServiceChanFull{Len: serviceBufferSize}, err)
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
	tracer, _ := getTestTracer()
	defer tracer.Stop()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		go func() {
			parent := tracer.newRootSpan("pylons.request", "pylons", "/")
			defer parent.Finish()

			for i := 0; i < 10; i++ {
				tracer.newChildSpan("redis.command", parent).Finish()
			}
		}()
	}
}

// BenchmarkTracerAddSpans tests the performance of creating and finishing a root
// span. It should include the encoding overhead.
func BenchmarkTracerAddSpans(b *testing.B) {
	tracer, _ := getTestTracer()
	defer tracer.Stop()

	for n := 0; n < b.N; n++ {
		span := tracer.newRootSpan("pylons.request", "pylons", "/")
		span.Finish()
	}
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer(opts ...Option) (*Tracer, *dummyTransport) {
	transport := &dummyTransport{getEncoder: msgpackEncoderFactory}
	o := append([]Option{WithTransport(transport)}, opts...)
	tracer := New(o...)
	return tracer, transport
}

// Mock Transport with a real Encoder
type dummyTransport struct {
	getEncoder encoderFactory
	traces     [][]*span
	services   map[string]service

	sync.RWMutex // required because of some poll-testing (eg: worker)
}

func (t *dummyTransport) sendTraces(traces [][]*span) (*http.Response, error) {
	t.Lock()
	t.traces = append(t.traces, traces...)
	t.Unlock()

	encoder := t.getEncoder()
	return nil, encoder.encodeTraces(traces)
}

func (t *dummyTransport) sendServices(services map[string]service) (*http.Response, error) {
	t.Lock()
	t.services = services
	t.Unlock()

	encoder := t.getEncoder()
	return nil, encoder.encodeServices(services)
}

func (t *dummyTransport) Traces() [][]*span {
	t.Lock()
	defer t.Unlock()

	traces := t.traces
	t.traces = nil
	return traces
}
