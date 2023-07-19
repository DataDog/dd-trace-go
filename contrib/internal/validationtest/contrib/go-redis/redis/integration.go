// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis"
	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis"
)

type Integration struct {
	client   *redistrace.Client
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
	return "go-redis/redis"
}

func (i *Integration) Init(t *testing.T) {
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

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.client.Set("test_key", "test_value", 0)
	i.client.Get("test_key")
	i.client.Incr("int_key")
	i.client.ClientList()
	i.numSpans += 4

	pipeline := i.client.Pipeline()
	pipeline.Expire("pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.(*redistrace.Pipeliner).ExecWithContext(context.Background())
	i.numSpans++

}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
