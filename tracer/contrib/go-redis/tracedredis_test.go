package tracedredis

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
)

const (
	debug = false
)

func TestClient(t *testing.T) {
	default_opt := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	}
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	client := NewTracedClient(default_opt, context.Background(), testTracer)
	client.Set("test_key", "test_value", 0)

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.GetMeta("host"), "localhost")
	assert.Equal(span.GetMeta("port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "set test_key test_value: ")
}

func TestChildSpan(t *testing.T) {
	default_opt := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	}
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	// Parent span
	ctx := context.Background()
	parent_span := testTracer.NewChildSpanFromContext("parent_span", ctx)
	ctx = tracer.ContextWithSpan(ctx, parent_span)
	client := NewTracedClient(default_opt, ctx, testTracer)
	client.Set("test_key", "test_value", 0)
	parent_span.Finish()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	child_span := spans[0]
	pspan := spans[1]
	assert.Equal(pspan.Name, "parent_span")
	assert.Equal(child_span.ParentID, pspan.SpanID)
	assert.Equal(child_span.Name, "redis.command")
	assert.Equal(child_span.GetMeta("host"), "localhost")
	assert.Equal(child_span.GetMeta("port"), "6379")
	assert.Equal(child_span.GetMeta("redis.raw_command"), "set test_key test_value: ")
}

func TestMultipleCommands(t *testing.T) {
	default_opt := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	}
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	client := NewTracedClient(default_opt, context.Background(), testTracer)
	client.Set("test_key", "test_value", 0)
	client.Get("test_key")
	client.Incr("int_key")
	client.ClientList()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 4)
	spans := traces[0]
	assert.Len(spans, 1)
	assert.Equal(traces[0][0].GetMeta("redis.raw_command"), "set test_key test_value: ")
	assert.Equal(traces[1][0].GetMeta("redis.raw_command"), "get test_key: ")
	assert.Equal(traces[2][0].GetMeta("redis.raw_command"), "incr int_key: 0")
	assert.Equal(traces[3][0].GetMeta("redis.raw_command"), "client list: ")

}

func TestError(t *testing.T) {
	default_opt := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	}
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	client := NewTracedClient(default_opt, context.Background(), testTracer)
	err := client.Get("non_existent_key")

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(span.Error, 1)
	assert.Equal(span.GetMeta("error.msg"), err.Err())
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.GetMeta("host"), "localhost")
	assert.Equal(span.GetMeta("port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "get non_existent_key: ")
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	transport := &dummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

// dummyTransport is a transport that just buffers spans and encoding
type dummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *dummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *dummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *dummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}
func (t *dummyTransport) SetHeader(key, value string) {}
