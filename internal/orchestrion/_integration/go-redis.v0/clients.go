// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"fmt"
	"testing"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseClients checks that clients built by redis.NewClient,
// redis.NewFailoverClient and the contrib's own NewClient each report exactly
// one span per command. The contrib calls redis.NewClient itself, so a rule
// that also wrapped that call would report two.
//
// The clients point at a closed port, so no server is needed.
type TestCaseClients struct {
	addr string
}

func (tc *TestCaseClients) Setup(_ context.Context, t *testing.T) {
	tc.addr = closedAddr(t)
}

func (tc *TestCaseClients) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	get := func(client *redis.Client, key string) {
		require.Error(t, client.WithContext(ctx).Get(key).Err())
		require.NoError(t, client.Close())
	}
	get(redis.NewClient(&redis.Options{Addr: tc.addr}), "new-client")
	get(redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    "master",
		SentinelAddrs: []string{tc.addr},
	}), "new-failover-client")

	contribClient := redistrace.NewClient(&redis.Options{Addr: tc.addr}, redistrace.WithService("contrib.client"))
	require.Error(t, contribClient.WithContext(ctx).Get("contrib-new-client").Err())
	require.NoError(t, contribClient.Close())
}

func (*TestCaseClients) ExpectedSpanCount() int {
	// test.root, then a redis.command span per client.
	return 4
}

func (*TestCaseClients) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "new-client", false),
				commandSpan("redis.client", "new-failover-client", false),
				commandSpan("contrib.client", "contrib-new-client", false),
			},
		},
	}
}

// commandSpan describes the redis.command span for a GET of key, with its
// redis.dial child when dial is set.
func commandSpan(service, key string, dial bool) *trace.Trace {
	span := &trace.Trace{
		Tags: map[string]any{
			"name":     "redis.command",
			"service":  service,
			"resource": "get",
			"type":     "redis",
		},
		Meta: map[string]string{
			"component":         "go-redis/redis",
			"redis.raw_command": getCommand(key),
		},
	}
	if dial {
		span.Children = trace.Traces{
			{
				Tags: map[string]any{
					"name":    "redis.dial",
					"service": service,
				},
			},
		}
	}
	return span
}

// getCommand is the redis.raw_command tag of a GET of key.
func getCommand(key string) string {
	return "get " + key + ": "
}

// closedAddr returns a local address nothing listens on, so dials fail fast.
func closedAddr(t *testing.T) string {
	return fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))
}
