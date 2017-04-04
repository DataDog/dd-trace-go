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
