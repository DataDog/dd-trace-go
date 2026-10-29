// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	"example.com/elasticsearchdep/esv6"
	"example.com/elasticsearchdep/esv7"
	"example.com/elasticsearchdep/esv8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that clients created in a dependency module, not
// in the module being built, are traced once per request.
type TestCaseDependency struct {
	addr string
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.addr = startFake(t, nil)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	v6, err := esv6.NewClient(tc.addr)
	require.NoError(t, err)
	perform(ctx, t, v6, "v6-dependency")

	v7, err := esv7.NewClient(tc.addr)
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-dependency")

	v8, err := esv8.NewClient(tc.addr)
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-dependency")
}

// ExpectedSpanCount is test.root, then an elasticsearch.query span and its
// net/http span for each request and for the GET / the v7 client sends first.
func (*TestCaseDependency) ExpectedSpanCount() int {
	return 1 + 2*(3+1)
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("v6-dependency"),
				httpSpan("v6-dependency"),
				querySpan("v7-dependency"),
				httpSpan("v7-dependency"),
				querySpan("v8-dependency"),
				httpSpan("v8-dependency"),
			},
		},
	}
}
