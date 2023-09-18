// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"context"
	"testing"
	"time"

	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis.v7"

	"github.com/go-redis/redis/v7"
)

type GoRedisV7 struct {
	client   *redis.Client
	numSpans int
	opts     []redistrace.ClientOption
}

func NewGoRedisV7() *GoRedisV7 {
	return &GoRedisV7{
		opts: make([]redistrace.ClientOption, 0),
	}
}

func (i *GoRedisV7) WithServiceName(name string) {
	i.opts = append(i.opts, redistrace.WithServiceName(name))
}

func (i *GoRedisV7) Name() string {
	return "go-redis/redis.v7"
}

func (i *GoRedisV7) Init(t *testing.T) {
	t.Helper()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}

	i.client = redistrace.NewClient(opts, i.opts...)

	i.client.Set("test_key", "test_value", 0)
	i.numSpans++

	t.Cleanup(func() {
		i.client.Close()
		i.numSpans = 0
	})
}

func (i *GoRedisV7) GenSpans(t *testing.T) {
	t.Helper()

	i.client.Set("test_key", "test_value", 0)
	i.client.Get("test_key")
	i.client.Incr("int_key")
	i.client.ClientList()
	i.numSpans += 4

	pipeline := i.client.Pipeline()
	pipeline.Expire("pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.ExecContext(context.Background())
	i.numSpans++
}

func (i *GoRedisV7) NumSpans() int {
	return i.numSpans
}
