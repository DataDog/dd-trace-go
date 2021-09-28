// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis_test

import (
	"context"
	"time"

	"github.com/go-redis/redis"

	redistrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/go-redis/redis"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
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
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

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
	c := redistrace.NewClient(opts, redistrace.WithServiceName("my-redis-service"))

	// open the pipeline
	pipe := c.Pipeline()

	// submit some commands
	pipe.Incr("pipeline_counter")
	pipe.Expire("pipeline_counter", time.Hour)

	// execute with trace
	pipe.Exec()
}
