// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package valkey

import (
	"context"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/require"
	testvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/valkey-io/valkey-go"
)

type TestCase struct {
	client valkey.Client
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	valkeyContainer, err := testvalkey.Run(ctx, "valkey/valkey:8-alpine")
	require.NoError(t, err)
	require.NoError(t, valkeyContainer.Start(ctx), "failed to start a valkey container")
	endpoint, err := valkeyContainer.Endpoint(ctx, "http")
	require.NoError(t, err)
	tc.client, err = valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{strings.TrimPrefix(endpoint, "http://")},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, valkeyContainer.Terminate(context.Background())) })
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
						"name":     "valkey.command",
						"service":  "valkey.client",
						"resource": "SET",
						"type":     "valkey",
					},
					Meta: map[string]string{
						"_dd.base_service":           "valkey-go.test",
						"component":                  "valkey-io/valkey-go",
						"db.system":                  "valkey",
						"db.valkey.client.cache.hit": "false",
						"out.db":                     "0",
						"out.host":                   "localhost",
						"span.kind":                  "client",
					},
				},
				{
					Tags: map[string]any{
						"name":     "valkey.command",
						"service":  "valkey.client",
						"resource": "GET",
						"type":     "valkey",
					},
					Meta: map[string]string{
						"_dd.base_service":           "valkey-go.test",
						"component":                  "valkey-io/valkey-go",
						"db.system":                  "valkey",
						"db.valkey.client.cache.hit": "false",
						"out.db":                     "0",
						"out.host":                   "localhost",
						"span.kind":                  "client",
					},
				},
			},
		},
	}
}
