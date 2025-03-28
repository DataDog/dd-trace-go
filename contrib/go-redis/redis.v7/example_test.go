// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis_test

import (
	"context"
	"time"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/go-redis/redis/v7"
)

// To start tracing Redis, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()

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
	tracer.Start()
	defer tracer.Stop()

	// create a client
	opts := &redis.Options{Addr: "127.0.0.1", Password: "", DB: 0}
	c := redistrace.NewClient(opts, redistrace.WithService("my-redis-service"))

	// open the pipeline
	pipe := c.Pipeline()

	// submit some commands
	pipe.Incr("pipeline_counter")
	pipe.Expire("pipeline_counter", time.Hour)

	// execute with trace
	pipe.ExecContext(context.Background())
}
