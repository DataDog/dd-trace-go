// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/trace"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type TestCase struct {
	server *testredis.RedisContainer
	*redis.Client
	key string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	uuid, err := uuid.NewRandom()
	require.NoError(t, err)
	tc.key = uuid.String()

	container, addr := containers.StartRedisTestContainer(t)
	tc.server = container

	tc.Client = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { assert.NoError(t, tc.Client.Close()) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.Client.Set(ctx, "test_key", "test_value", 0).Err())
	require.NoError(t, tc.Client.Get(ctx, "test_key").Err())
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "redis.command",
						"service":  "redis.client",
						"resource": "set",
						"type":     "redis",
					},
					Meta: map[string]string{
						"redis.args_length": "3",
						"component":         "go-redis/redis.v8",
						"out.db":            "0",
						"span.kind":         "client",
						"db.system":         "redis",
						"redis.raw_command": "set test_key test_value:",
						"out.host":          "localhost",
					},
				},
				{
					Tags: map[string]any{
						"name":     "redis.command",
						"service":  "redis.client",
						"resource": "get",
						"type":     "redis",
					},
					Meta: map[string]string{
						"redis.args_length": "2",
						"component":         "go-redis/redis.v8",
						"out.db":            "0",
						"span.kind":         "client",
						"db.system":         "redis",
						"redis.raw_command": "get test_key:",
						"out.host":          "localhost",
					},
				},
			},
		},
	}
}
