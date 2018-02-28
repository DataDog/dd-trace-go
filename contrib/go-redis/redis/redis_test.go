package redis

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/ddtrace/tracer"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
)

const debug = false

func TestClient(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	client.Set("test_key", "test_value", 0)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("set test_key test_value: ", span.Tag("redis.raw_command"))
	assert.Equal("3", span.Tag("redis.args_length"))
}

func TestPipeline(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	pipeline := client.Pipeline()
	pipeline.Expire("pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.ExecWithContext(context.Background())

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("expire pipeline_counter 3600: false\n", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("1", span.Tag("redis.pipeline_length"))

	mt.Reset()
	pipeline.Expire("pipeline_counter", time.Hour)
	pipeline.Expire("pipeline_counter_1", time.Minute)

	// Rewriting Exec
	pipeline.Exec()

	spans = mt.FinishedSpans()
	assert.Len(spans, 1)

	span = spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("expire pipeline_counter 3600: false\nexpire pipeline_counter_1 60: false\n", span.Tag(ext.ResourceName))
	assert.Equal("2", span.Tag("redis.pipeline_length"))
}

func TestChildSpan(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	client := NewClient(opts, WithServiceName("my-redis"))
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.span")
	client = client.WithContext(ctx)
	client.Set("test_key", "test_value", 0)
	root.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var child, parent mocktracer.Span
	for _, s := range spans {
		// order of traces in buffer is not garanteed
		switch s.OperationName() {
		case "redis.command":
			child = s
		case "parent.span":
			parent = s
		}
	}
	assert.NotNil(parent)
	assert.NotNil(child)

	assert.Equal(child.ParentID(), parent.SpanID())
	assert.Equal(child.Tag(ext.TargetHost), "127.0.0.1")
	assert.Equal(child.Tag(ext.TargetPort), "6379")
}

func TestMultipleCommands(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	client.Set("test_key", "test_value", 0)
	client.Get("test_key")
	client.Incr("int_key")
	client.ClientList()

	spans := mt.FinishedSpans()
	assert.Len(spans, 4)

	// Checking all commands were recorded
	var commands [4]string
	for i := 0; i < 4; i++ {
		commands[i] = spans[i].Tag("redis.raw_command").(string)
	}
	assert.Contains(commands, "set test_key test_value: ")
	assert.Contains(commands, "get test_key: ")
	assert.Contains(commands, "incr int_key: 0")
	assert.Contains(commands, "client list: ")
}

func TestError(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	err := client.Get("non_existent_key")

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal("redis.command", span.OperationName())
	assert.Equal(err.Err().(error), span.Tag(ext.Error))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("get non_existent_key: ", span.Tag("redis.raw_command"))
}
