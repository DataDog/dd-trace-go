package redis_test

import (
	"context"
	"time"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-redis/redis"
)

// To start tracing Redis, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	// create a new Client
	opts := &redis.Options{Addr: "127.0.0.1", Password: "", DB: 0}
	c := redistrace.NewClient(opts)

	// any action emits a span
	c.Set("test_key", "test_value", 0)

	// optionally, create a new root span
	root := tracer.NewRootSpan("parent.request", "web", "/home")

	// and attach it to a context
	ctx := root.Context(context.Background())

	// set the context on the client
	c = c.WithContext(ctx)

	// commit further commands, which will inherit from the parent in the context.
	c.Set("food", "cheese", 0)
	root.Finish()
}

// You can also trace Redis Pipelines. Simply use as usual and the traces will be
// automatically picked up by the underlying implementation.
func Example_pipeliner() {
	// create a client
	opts := &redis.Options{Addr: "127.0.0.1", Password: "", DB: 0}
	c := redistrace.NewClient(opts)

	// open the pipeline
	pipe := c.Pipeline()

	// submit some commands
	pipe.Incr("pipeline_counter")
	pipe.Expire("pipeline_counter", time.Hour)

	// execute with trace
	pipe.Exec()
}
