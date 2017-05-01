package goredistrace_test

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/go-redis"
	redis "github.com/go-redis/redis"
	"time"
)

// To start tracing Redis commands, use the NewTracedClient function to create a traced Redis clienty,
// passing in a service name of choice.
func Example() {
	opts := &redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "", // no password set
		DB:       0,  // use default db
	}
	c := goredistrace.NewTracedClient(opts, tracer.DefaultTracer, "my-redis-backend")
	// Emit spans per command by using your Redis connection as usual
	c.Set("test_key", "test_value", 0)

	// Use a context to pass information down the call chain
	root := tracer.NewRootSpan("parent.request", "web", "/home")
	ctx := root.Context(context.Background())

	// When set with a context, the traced client will emit a span inheriting from 'parent.request'
	c.SetContext(ctx)
	c.Set("food", "cheese", 0)
	root.Finish()
}

// You can also trace Redis Pipelines
func Example_pipeline() {
	opts := &redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "", // no password set
		DB:       0,  // use default db
	}
	c := goredistrace.NewTracedClient(opts, tracer.DefaultTracer, "my-redis-backend")
	// p is a TracedPipeline
	pipe := c.Pipeline()
	pipe.Incr("pipeline_counter")
	pipe.Expire("pipeline_counter", time.Hour)

	pipe.Exec()
}

func ExampleNewTracedClient() {
	opts := &redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "", // no password set
		DB:       0,  // use default db
	}
	c := goredistrace.NewTracedClient(opts, tracer.DefaultTracer, "my-redis-backend")
	// Emit spans per command by using your Redis connection as usual
	c.Set("test_key", "test_value", 0)

	// Use a context to pass information down the call chain
	root := tracer.NewRootSpan("parent.request", "web", "/home")
	ctx := root.Context(context.Background())

	// When set with a context, the traced client will emit a span inheriting from 'parent.request'
	c.SetContext(ctx)
	c.Set("food", "cheese", 0)
	root.Finish()
}
