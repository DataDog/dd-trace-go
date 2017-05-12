package tracer

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultTracer(t *testing.T) {
	assert := assert.New(t)

	var wg sync.WaitGroup

	// the default client must be available
	assert.NotNil(DefaultTracer)

	// package free functions must proxy the calls to the
	// default client
	root := NewRootSpan("pylons.request", "pylons", "/")
	NewChildSpan("pylons.request", root)

	wg.Add(2)

	go func() {
		for i := 0; i < 1000; i++ {
			Disable()
			Enable()
		}
		wg.Done()
	}()

	go func() {
		for i := 0; i < 1000; i++ {
			_ = DefaultTracer.Enabled()
		}
		wg.Done()
	}()

	wg.Wait()
}

func TestNewSpan(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create root spans
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")
	assert.Equal(span.ParentID, uint64(0))
	assert.Equal(span.Service, "pylons")
	assert.Equal(span.Name, "pylons.request")
	assert.Equal(span.Resource, "/")
}

func TestNewSpanFromContextNil(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()

	child := tracer.NewChildSpanFromContext("abc", nil)
	assert.Equal(child.Name, "abc")
	assert.Equal(child.Service, "")

	child = tracer.NewChildSpanFromContext("def", context.Background())
	assert.Equal(child.Name, "def")
	assert.Equal(child.Service, "")

}

func TestSpan(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()

	// nil context
	span, ctx := tracer.Span("abc", nil)
	assert.Equal("abc", span.Name)
	assert.Equal("", span.Service)
	assert.Equal(span.ParentID, span.SpanID) // it should be a root span
	// the returned ctx should contain the created span
	assert.NotNil(ctx)
	ctxSpan, ok := SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(span, ctxSpan)

	// context without span
	span, ctx = tracer.Span("abc", context.Background())
	assert.Equal("abc", span.Name)
	assert.Equal("", span.Service)
	assert.Equal(span.ParentID, span.SpanID) // it should be a root span
	// the returned ctx should contain the created span
	assert.NotNil(ctx)
	ctxSpan, ok = SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(span, ctxSpan)

	// context with span
	parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
	parentCTX := ContextWithSpan(context.Background(), parent)
	span, ctx = tracer.Span("def", parentCTX)
	assert.Equal("def", span.Name)
	assert.Equal("pylons", span.Service)
	assert.Equal(parent.Service, span.Service)
	// the created span should be a child of the parent span
	assert.Equal(span.ParentID, parent.SpanID)
	// the returned ctx should contain the created span
	assert.NotNil(ctx)
	ctxSpan, ok = SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(ctxSpan, span)
}

func TestNewSpanFromContext(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create child spans
	tracer := NewTracer()
	parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
	ctx := ContextWithSpan(context.Background(), parent)

	child := tracer.NewChildSpanFromContext("redis.command", ctx)
	// ids and services are inherited
	assert.Equal(child.ParentID, parent.SpanID)
	assert.Equal(child.TraceID, parent.TraceID)
	assert.Equal(child.Service, parent.Service)
	// the resource is not inherited and defaults to the name
	assert.Equal(child.Resource, "redis.command")
	// the tracer instance is the same
	assert.Equal(parent.tracer, tracer)
	assert.Equal(child.tracer, tracer)

}

func TestNewSpanChild(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create child spans
	tracer := NewTracer()
	parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
	child := tracer.NewChildSpan("redis.command", parent)
	// ids and services are inherited
	assert.Equal(child.ParentID, parent.SpanID)
	assert.Equal(child.TraceID, parent.TraceID)
	assert.Equal(child.Service, parent.Service)
	// the resource is not inherited and defaults to the name
	assert.Equal(child.Resource, "redis.command")
	// the tracer instance is the same
	assert.Equal(parent.tracer, tracer)
	assert.Equal(child.tracer, tracer)
}

func TestTracerDisabled(t *testing.T) {
	assert := assert.New(t)

	// disable the tracer and be sure that the span is not added
	tracer := NewTracer()
	tracer.SetEnabled(false)
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")
	span.Finish()
	assert.Equal(tracer.buffer.Len(), 0)
}

func TestTracerEnabledAgain(t *testing.T) {
	assert := assert.New(t)

	// disable the tracer and enable it again
	tracer := NewTracer()
	tracer.SetEnabled(false)
	preSpan := tracer.NewRootSpan("pylons.request", "pylons", "/")
	preSpan.Finish()
	tracer.SetEnabled(true)
	postSpan := tracer.NewRootSpan("pylons.request", "pylons", "/")
	postSpan.Finish()
	assert.Equal(tracer.buffer.Len(), 1)
}

func TestTracerSampler(t *testing.T) {
	assert := assert.New(t)

	sampleRate := 0.5
	tracer := NewTracer()
	tracer.SetSampleRate(sampleRate)

	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// The span might be sampled or not, we don't know, but at least it should have the sample rate metric
	assert.Equal(sampleRate, span.Metrics[sampleRateMetricKey])
}

func TestTracerEdgeSampler(t *testing.T) {
	assert := assert.New(t)

	// a sample rate of 0 should sample nothing
	tracer0 := NewTracer()
	tracer0.SetSampleRate(0)
	// a sample rate of 1 should sample everything
	tracer1 := NewTracer()
	tracer1.SetSampleRate(1)

	count := 10000

	for i := 0; i < count; i++ {
		span0 := tracer0.NewRootSpan("pylons.request", "pylons", "/")
		span0.Finish()
		span1 := tracer1.NewRootSpan("pylons.request", "pylons", "/")
		span1.Finish()
	}

	assert.Equal(0, tracer0.buffer.Len())
	assert.Equal(count, tracer1.buffer.Len())
}

func TestTracerBuffer(t *testing.T) {
	assert := assert.New(t)

	bufferSize := 1000
	incorrectBufferSize := -1
	defaultBufferSize := 10000

	tracer0 := NewTracer()
	tracer0.SetSpansBufferSize(bufferSize)

	tracer1 := NewTracer()
	tracer1.SetSpansBufferSize(incorrectBufferSize)

	assert.Equal(bufferSize, tracer0.buffer.maxSize)
	assert.Equal(defaultBufferSize, tracer1.buffer.maxSize)
}

func TestTracerConcurrent(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()

	// Wait for three different goroutines that should create
	// three different traces with one child each
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		tracer.NewRootSpan("pylons.request", "pylons", "/").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.NewRootSpan("pylons.request", "pylons", "/home").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.NewRootSpan("pylons.request", "pylons", "/trace").Finish()
	}()

	wg.Wait()
	tracer.FlushTraces()
	traces := transport.Traces()
	assert.Len(traces, 3)
	assert.Len(traces[0], 1)
	assert.Len(traces[1], 1)
	assert.Len(traces[2], 1)
}

func TestTracerConcurrentMultipleSpans(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()

	// Wait for two different goroutines that should create
	// two traces with two children each
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
		child := tracer.NewChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()
	go func() {
		defer wg.Done()
		parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
		child := tracer.NewChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()

	wg.Wait()
	tracer.FlushTraces()
	traces := transport.Traces()
	assert.Len(traces, 2)
	assert.Len(traces[0], 2)
	assert.Len(traces[1], 2)
}

func TestTracerServices(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()

	tracer.SetServiceInfo("svc1", "a", "b")
	tracer.SetServiceInfo("svc2", "c", "d")
	tracer.SetServiceInfo("svc1", "e", "f")

	tracer.Stop()

	assert.Equal(2, len(transport.services))

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

func TestTracerServicesDisabled(t *testing.T) {
	assert := assert.New(t)
	tracer, transport := getTestTracer()

	tracer.SetEnabled(false)
	tracer.SetServiceInfo("svc1", "a", "b")
	tracer.Stop()

	assert.Equal(0, len(transport.services))
}

func TestTracerMeta(t *testing.T) {
	assert := assert.New(t)

	var nilTracer *Tracer
	nilTracer.SetMeta("key", "value")
	assert.Nil(nilTracer.getAllMeta(), "nil tracer should return nil meta")

	tracer, _ := getTestTracer()
	assert.Nil(tracer.getAllMeta(), "by default, no meta")
	tracer.SetMeta("env", "staging")

	span := tracer.NewRootSpan("pylons.request", "pylons", "/")
	assert.Equal("staging", span.GetMeta("env"))
	assert.Equal("", span.GetMeta("component"))
	span.Finish()
	assert.Equal(map[string]string{"env": "staging"}, tracer.getAllMeta(), "there should be one meta")

	tracer.SetMeta("component", "core")
	span = tracer.NewRootSpan("pylons.request", "pylons", "/")
	assert.Equal("staging", span.GetMeta("env"))
	assert.Equal("core", span.GetMeta("component"))
	span.Finish()
	assert.Equal(map[string]string{"env": "staging", "component": "core"}, tracer.getAllMeta(), "there should be two entries")

	tracer.SetMeta("env", "prod")
	span = tracer.NewRootSpan("pylons.request", "pylons", "/")
	assert.Equal("prod", span.GetMeta("env"))
	assert.Equal("core", span.GetMeta("component"))
	span.SetMeta("env", "sandbox")
	assert.Equal("sandbox", span.GetMeta("env"))
	assert.Equal("core", span.GetMeta("component"))
	span.Finish()

	assert.Equal(map[string]string{"env": "prod", "component": "core"}, tracer.getAllMeta(), "key1 should have been updated")
}

// BenchmarkConcurrentTracing tests the performance of spawning a lot of
// goroutines where each one creates a trace with a parent and a child.
func BenchmarkConcurrentTracing(b *testing.B) {
	tracer, _ := getTestTracer()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		go func() {
			parent := tracer.NewRootSpan("pylons.request", "pylons", "/")
			defer parent.Finish()

			for i := 0; i < 10; i++ {
				tracer.NewChildSpan("redis.command", parent).Finish()
			}
		}()
	}
}

// BenchmarkTracerAddSpans tests the performance of creating and finishing a root
// span. It should include the encoding overhead.
func BenchmarkTracerAddSpans(b *testing.B) {
	tracer, _ := getTestTracer()

	for n := 0; n < b.N; n++ {
		span := tracer.NewRootSpan("pylons.request", "pylons", "/")
		span.Finish()
	}
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer() (*Tracer, *dummyTransport) {
	pool, _ := newEncoderPool(MSGPACK_ENCODER, encoderPoolSize)
	transport := &dummyTransport{pool: pool}
	tracer := NewTracerTransport(transport)
	return tracer, transport
}

// Mock Transport with a real Encoder
type dummyTransport struct {
	pool     *encoderPool
	traces   [][]*Span
	services map[string]Service
}

func (t *dummyTransport) SendTraces(traces [][]*Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)
	return nil, encoder.EncodeTraces(traces)
}

func (t *dummyTransport) SendServices(services map[string]Service) (*http.Response, error) {
	t.services = services
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)
	return nil, encoder.EncodeServices(services)
}

func (t *dummyTransport) Traces() [][]*Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *dummyTransport) SetHeader(key, value string) {}
