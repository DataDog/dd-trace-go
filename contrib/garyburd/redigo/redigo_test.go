package redigo

import (
	"context"
	"fmt"
	"testing"

	"github.com/garyburd/redigo/redis"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"
)

const debug = false

func TestClient(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	c, err := DialWithServiceName("my-service", testTracer, "tcp", "127.0.0.1:6379")
	assert.Nil(err)
	c.Do("SET", 1, "truck")

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]

	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.Service, "my-service")
	assert.Equal(span.Resource, "SET")
	assert.Equal(span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(span.GetMeta("out.port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "SET 1 truck")
	assert.Equal(span.GetMeta("redis.args_length"), "2")
}

func TestCommandError(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	c, err := DialWithServiceName("my-service", testTracer, "tcp", "127.0.0.1:6379")
	assert.Nil(err)
	_, err = c.Do("NOT_A_COMMAND", context.Background())
	assert.NotNil(err)

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(int32(span.Error), int32(1))
	assert.Equal(span.GetMeta("error.msg"), err.Error())
	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.Service, "my-service")
	assert.Equal(span.Resource, "NOT_A_COMMAND")
	assert.Equal(span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(span.GetMeta("out.port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "NOT_A_COMMAND")
}

func TestConnectionError(t *testing.T) {
	assert := assert.New(t)
	testTracer, _ := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	_, err := DialWithServiceName("redis-service", testTracer, "tcp", "127.0.0.1:1000")

	assert.NotNil(err)
	assert.Contains(err.Error(), "dial tcp 127.0.0.1:1000")
}

func TestInheritance(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	// Parent span
	ctx := context.Background()
	parentSpan := testTracer.NewChildSpanFromContext("parentSpan", ctx)
	ctx = tracer.ContextWithSpan(ctx, parentSpan)
	client, err := DialWithServiceName("my_service", testTracer, "tcp", "127.0.0.1:6379")
	assert.Nil(err)
	client.Do("SET", "water", "bottle", ctx)
	parentSpan.Finish()

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	var child_span, pspan *tracer.Span
	for _, s := range spans {
		// order of traces in buffer is not garanteed
		switch s.Name {
		case "redis.command":
			child_span = s
		case "parentSpan":
			pspan = s
		}
	}
	assert.NotNil(child_span, "there should be a child redis.command span")
	assert.NotNil(child_span, "there should be a parent span")

	assert.Equal(child_span.ParentID, pspan.SpanID)
	assert.Equal(child_span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(child_span.GetMeta("out.port"), "6379")
}

type stringifyTest struct{ A, B int }

func (ts stringifyTest) String() string { return fmt.Sprintf("[%d, %d]", ts.A, ts.B) }

func TestCommandsToSring(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	str := stringifyTest{A: 57, B: 8}
	c, err := DialWithServiceName("my-service", testTracer, "tcp", "127.0.0.1:6379")
	assert.Nil(err)
	c.Do("SADD", "testSet", "a", int(0), int32(1), int64(2), str, context.Background())

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(span.Name, "redis.command")
	assert.Equal(span.Service, "my-service")
	assert.Equal(span.Resource, "SADD")
	assert.Equal(span.GetMeta("out.host"), "127.0.0.1")
	assert.Equal(span.GetMeta("out.port"), "6379")
	assert.Equal(span.GetMeta("redis.raw_command"), "SADD testSet a 0 1 2 [57, 8]")
}

func TestPool(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)

	pool := &redis.Pool{
		MaxIdle:     2,
		MaxActive:   3,
		IdleTimeout: 23,
		Wait:        true,
		Dial: func() (redis.Conn, error) {
			return DialWithServiceName("my-service", testTracer, "tcp", "127.0.0.1:6379")
		},
	}

	pc := pool.Get()
	pc.Do("SET", " whiskey", " glass", context.Background())
	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(span.GetMeta("out.network"), "tcp")
}

func TestTracingDialUrl(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetDebugLogging(debug)
	url := "redis://127.0.0.1:6379"
	client, err := DialURLWithServiceName("redis-service", testTracer, url)
	assert.Nil(err)
	client.Do("SET", "ONE", " TWO", context.Background())

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
}
