// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package redis

import (
	"context"
	"testing"
	"time"

	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/redis/go-redis.v9"

	"github.com/redis/go-redis/v9"
)

type Integration struct {
	client   redis.UniversalClient
	numSpans int
	opts     []redistrace.ClientOption
}

func New() *Integration {
	return &Integration{
		opts: make([]redistrace.ClientOption, 0),
	}
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, redistrace.WithServiceName(name))
}

func (i *Integration) Name() string {
	return "redis/go-redis.v9"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.client = redistrace.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, i.opts...)
	i.numSpans++

	t.Cleanup(func() {
		i.client.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
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

func (i *Integration) NumSpans() int {
	return i.numSpans
}
