// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package twirp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	client example.Haberdasher
	addr   string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.addr, tc.client = Setup(t)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	_, err := tc.client.MakeHat(ctx, &example.Size{Inches: 6})
	require.NoError(t, err)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"service":  "twirp.test",
				"resource": "POST /twirp/twitch.twirp.example.Haberdasher/MakeHat",
				"type":     "http",
			},
			Meta: map[string]string{
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"service":  "http.router",
						"resource": "POST /twirp/twitch.twirp.example.Haberdasher/MakeHat",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "twirp.Haberdasher",
								"service":  "twirp-server",
								"resource": "MakeHat",
								"type":     "web",
							},
							Meta: map[string]string{
								"component":     "twitchtv/twirp",
								"rpc.system":    "twirp",
								"rpc.service":   "Haberdasher",
								"rpc.method":    "MakeHat",
								"twirp.method":  "MakeHat",
								"twirp.package": "twitch.twirp.example",
								"twirp.service": "Haberdasher",
							},
						},
					},
				},
			},
		},
	}
}
