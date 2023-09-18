// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"context"
	"testing"
	"time"

	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis.v8"

	"github.com/go-redis/redis/v8"
)

type GoRedisV8 struct {
	client   redis.UniversalClient
	numSpans int
	opts     []redistrace.ClientOption
}

func NewGoRedisV8() *GoRedisV8 {
	return &GoRedisV8{
		opts: make([]redistrace.ClientOption, 0),
	}
}

func (i *GoRedisV8) WithServiceName(name string) {
	i.opts = append(i.opts, redistrace.WithServiceName(name))
}

func (i *GoRedisV8) Name() string {
	return "go-redis/redis.v8"
}

func (i *GoRedisV8) Init(t *testing.T) {
	t.Helper()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}

	i.client = redistrace.NewClient(opts, i.opts...)

	t.Cleanup(func() {
		i.client.Close()
		i.numSpans = 0
	})
}

func (i *GoRedisV8) GenSpans(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	i.client.Set(ctx, "test_key", "test_value", 0)
	i.client.Get(ctx, "test_key")
	i.client.Incr(ctx, "int_key")
	i.client.ClientList(ctx)
	i.numSpans += 4

	pipeline := i.client.Pipeline()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.Exec(ctx)
	i.numSpans++
}

func (i *GoRedisV8) NumSpans() int {
	return i.numSpans
}
