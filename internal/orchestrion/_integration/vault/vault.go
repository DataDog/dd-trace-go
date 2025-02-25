// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package vault

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testvault "github.com/testcontainers/testcontainers-go/modules/vault"
)

type TestCase struct {
	server *testvault.VaultContainer
	*api.Client
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	tc.server, err = testvault.Run(ctx,
		"vault:1.7.3", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		containers.WithTestLogConsumer(t),
		testvault.WithToken("root"),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, tc.server)

	addr, err := tc.server.HttpHostAddress(ctx)
	if err != nil {
		defer tc.server.Terminate(ctx)
		t.Skipf("Failed to get vault container address: %v\n", err)
	}
	c, err := api.NewClient(&api.Config{
		Address: addr,
	})
	c.SetToken("root")
	if err != nil {
		t.Fatal(err)
	}
	tc.Client = c
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	_, err := tc.Logical().ReadWithContext(ctx, "secret/key")
	require.NoError(t, err)
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
						"name":     "vault.command",
						"service":  "vault",
						"resource": "GET /v1/secret/key",
						"type":     "http",
					},
					Meta: map[string]string{
						"http.method": "GET",
						"http.url":    "/v1/secret/key",
						"span.kind":   "client",
					},
				},
			},
		},
	}
}
