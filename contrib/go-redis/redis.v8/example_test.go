// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis_test

import (
	"context"
	"time"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/go-redis/redis/v8"
)

// To start tracing Redis, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	ctx := context.Background()
	// create a new Client
	opts := &redis.Options{Addr: "127.0.0.1", Password: "", DB: 0}
	c := redistrace.NewClient(opts)

	// any action emits a span
	c.Set(ctx, "test_key", "test_value", 0)

	// optionally, create a new root span
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	// commit further commands, which will inherit from the parent in the context.
	c.Set(ctx, "food", "cheese", 0)
	root.Finish()
}

// You can also trace Redis Pipelines. Simply use as usual and the traces will be
// automatically picked up by the underlying implementation.
func Example_pipeliner() {
	tracer.Start()
	defer tracer.Stop()

	ctx := context.Background()
	// create a client
	opts := &redis.Options{Addr: "127.0.0.1", Password: "", DB: 0}
	c := redistrace.NewClient(opts, redistrace.WithService("my-redis-service"))

	// open the pipeline
	pipe := c.Pipeline()

	// submit some commands
	pipe.Incr(ctx, "pipeline_counter")
	pipe.Expire(ctx, "pipeline_counter", time.Hour)

	// execute with trace
	pipe.Exec(ctx)
}

// You can create a traced ClusterClient using WrapClient
func Example_wrapClient() {
	tracer.Start()
	defer tracer.Stop()

	c := redis.NewClusterClient(&redis.ClusterOptions{})
	redistrace.WrapClient(c)

	//Do something, passing in any relevant context
	c.Incr(context.TODO(), "my_counter")
}
