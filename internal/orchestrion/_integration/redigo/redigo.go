// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package redigo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/cenkalti/backoff/v4"
	"github.com/gomodule/redigo/redis"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type TestCase struct {
	server *testredis.RedisContainer
	*redis.Pool
	key string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	uuid, err := uuid.NewRandom()
	require.NoError(t, err)
	tc.key = uuid.String()

	container, addr := containers.StartRedisTestContainer(t)
	tc.server = container

	const network = "tcp"

	var dialOptions = []redis.DialOption{
		redis.DialReadTimeout(10 * time.Second),
	}

	tc.Pool = &redis.Pool{
		Dial: func() (redis.Conn, error) { return redis.Dial(network, addr, dialOptions...) },
		DialContext: func(ctx context.Context) (redis.Conn, error) {
			return redis.DialContext(ctx, network, addr)
		},
		TestOnBorrow: func(c redis.Conn, _ time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
	t.Cleanup(func() { assert.NoError(t, tc.Pool.Close()) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client, err := tc.Pool.GetContext(ctx)
	require.NoError(t, err)
	defer func() { assert.NoError(t, client.Close()) }()

	_, err = backoff.RetryWithData(
		func() (any, error) {
			res, err := client.Do("SET", tc.key, "test_value")
			if err != nil {
				// If there was an error, replace the client with a new one...
				err = errors.Join(err, client.Close()) // Close the old clien
				newC, newE := tc.Pool.GetContext(ctx)
				client = newC
				err = errors.Join(err, newE)
			}
			return res, err
		},
		backoff.NewExponentialBackOff(),
	)
	require.NoError(t, err)

	res, err := client.Do("GET", tc.key, ctx)
	require.NoError(t, err)
	require.NotEmpty(t, res)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"resource": "SET",
						"type":     "redis",
						"name":     "redis.command",
						"service":  "redis.conn",
					},
					Meta: map[string]string{
						"redis.raw_command": fmt.Sprintf("SET %s test_value", tc.key),
						"db.system":         "redis",
						"component":         "gomodule/redigo",
						"out.network":       "tcp",
						"out.host":          "localhost",
						"redis.args_length": "2",
						"span.kind":         "client",
					},
				},
				{
					Tags: map[string]any{
						"resource": "GET",
						"type":     "redis",
						"name":     "redis.command",
						"service":  "redis.conn",
					},
					Meta: map[string]string{
						"redis.raw_command": fmt.Sprintf("GET %s", tc.key),
						"db.system":         "redis",
						"component":         "gomodule/redigo",
						"out.network":       "tcp",
						"out.host":          "localhost",
						"redis.args_length": "1",
						"span.kind":         "client",
					},
				},
				{
					Tags: map[string]any{
						"resource": "redigo.Conn.Flush",
						"type":     "redis",
						"name":     "redis.command",
						"service":  "redis.conn",
					},
					Meta: map[string]string{
						"redis.raw_command": "",
						"db.system":         "redis",
						"component":         "gomodule/redigo",
						"out.network":       "tcp",
						"out.host":          "localhost",
						"redis.args_length": "0",
						"span.kind":         "client",
					},
				},
			},
		},
	}
}
