// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	client rueidis.Client
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartRedisTestContainer(t)
	var err error
	tc.client, err = rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{addr},
	})
	require.NoError(t, err)
	t.Cleanup(func() { tc.client.Close() })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.NoError(t, tc.client.Do(ctx, tc.client.B().Set().Key("key").Value("value").Build()).Error())
	require.NoError(t, tc.client.Do(ctx, tc.client.B().Get().Key("key").Build()).Error())
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
						"name":     "redis.command",
						"service":  "redis.client",
						"resource": "SET",
						"type":     "redis",
					},
					Meta: map[string]string{
						"_dd.base_service":          "rueidis.test",
						"component":                 "redis/rueidis",
						"db.system":                 "redis",
						"db.redis.client.cache.hit": "false",
						"out.db":                    "0",
						"out.host":                  "localhost",
						"span.kind":                 "client",
					},
				},
				{
					Tags: map[string]any{
						"name":     "redis.command",
						"service":  "redis.client",
						"resource": "GET",
						"type":     "redis",
					},
					Meta: map[string]string{
						"_dd.base_service":          "rueidis.test",
						"component":                 "redis/rueidis",
						"db.system":                 "redis",
						"db.redis.client.cache.hit": "false",
						"out.db":                    "0",
						"out.host":                  "localhost",
						"span.kind":                 "client",
					},
				},
			},
		},
	}
}
