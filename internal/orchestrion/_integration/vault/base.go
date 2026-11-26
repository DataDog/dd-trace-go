// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// spansPerRequest is the root span, the vault span, the net/http client span
// and the net/http server span of one read.
const spansPerRequest = 4

// base runs a read against an in-process server that answers like Vault, so
// the cases below need no container.
type base struct {
	server *httptest.Server
	client *api.Client
}

func (b *base) setup(t *testing.T) {
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"value":"bar"}}`))
	}))
	t.Cleanup(b.server.Close)
}

// newClient builds the client from cfg. cfg must come from a literal in the
// calling test case, since that is what the rules rewrite.
func (b *base) newClient(t *testing.T, cfg *api.Config) {
	c, err := api.NewClient(cfg)
	require.NoError(t, err)
	b.client = c
}

func (b *base) run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	secret, err := b.client.Logical().ReadWithContext(ctx, "secret/key")
	require.NoError(t, err)
	require.NotNil(t, secret)
	require.Equal(t, "bar", secret.Data["value"])
}

func (*base) expectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"service":  "vault",
						"resource": "GET /v1/secret/key",
						"type":     "http",
					},
					Meta: map[string]string{
						"component":   "hashicorp/vault",
						"http.method": "GET",
						"http.url":    "/v1/secret/key",
						"span.kind":   "client",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name": "http.request",
								"type": "http",
							},
							Meta: map[string]string{
								"component": "net/http",
								"span.kind": "client",
							},
							Children: trace.Traces{
								{
									Tags: map[string]any{
										"name":     "http.request",
										"resource": "GET /v1/secret/key",
										"type":     "web",
									},
									Meta: map[string]string{
										"component": "net/http",
										"span.kind": "server",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
