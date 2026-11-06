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

	redistrace "github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2"
	"github.com/redis/go-redis/v9"
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
// The clients point at a closed port, so no server is needed. A *redis.Client
// also reports a failed redis.dial span under each redis.command span.
type TestCaseClients struct {
	addr string
}

func (tc *TestCaseClients) Setup(_ context.Context, t *testing.T) {
	tc.addr = closedAddr(t)
}

func (tc *TestCaseClients) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	clients := map[string]redis.UniversalClient{
		"new-client": redis.NewClient(&redis.Options{Addr: tc.addr, MaxRetries: -1}),
		"new-failover-client": redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    "master",
			SentinelAddrs: []string{tc.addr},
			MaxRetries:    -1,
		}),
		"contrib-new-client": redistrace.NewClient(&redis.Options{Addr: tc.addr, MaxRetries: -1}, redistrace.WithService("contrib.client")),
	}
	for key, client := range clients {
		require.Error(t, client.Do(ctx, "get", key).Err())
		require.NoError(t, client.Close())
	}
}

func (*TestCaseClients) ExpectedSpanCount() int {
	// test.root, then a redis.command and a redis.dial span per client.
	return 7
}

func (*TestCaseClients) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "new-client", true),
				commandSpan("redis.client", "new-failover-client", true),
				commandSpan("contrib.client", "contrib-new-client", true),
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
			"component":         "redis/go-redis.v9",
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
	return "get " + key
}

// closedAddr returns a local address nothing listens on, so dials fail fast.
func closedAddr(t *testing.T) string {
	return fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))
}
