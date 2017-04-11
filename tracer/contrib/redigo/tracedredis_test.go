package tracedredis

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
)

const (
	debug = false
)

func TestClient(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	c, _ := TracedDial(testTracer, "tcp", "127.0.0.1:6379")
	c.TraceDo(context.Background(), "SET", "fleet", "truck")

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]

	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(span.GetMeta("out.port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "SET fleet truck")
	assert.Equal(span.GetMeta("redis.args_length"), "2")
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	c, _ := TracedDial(testTracer, "tcp", "127.0.0.1:6379")
	_, err := c.TraceDo(context.Background(), "NOT_A_COMMAND")

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(int32(span.Error), int32(1))
	assert.Equal(span.GetMeta("error.msg"), err.Error())
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(span.GetMeta("out.port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "NOT_A_COMMAND")
}

func TestInheritance(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	// Parent span
	ctx := context.Background()
	parent_span := testTracer.NewChildSpanFromContext("parent_span", ctx)
	ctx = tracer.ContextWithSpan(ctx, parent_span)
	client, _ := TracedDial(testTracer, "tcp", "127.0.0.1:6379")
	client.TraceDo(ctx, "SET water bottle")
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
	assert.Equal(child_span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(child_span.GetMeta("out.port"), "6379")
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
