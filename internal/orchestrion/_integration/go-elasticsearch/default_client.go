// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	elasticsearch6 "github.com/elastic/go-elasticsearch/v6"
	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseDefaultClient checks that a client built by NewDefaultClient in each
// major reports exactly one elasticsearch.query span per request.
type TestCaseDefaultClient struct{}

func (*TestCaseDefaultClient) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion does not trace NewDefaultClient, whose Config literal is inside go-elasticsearch")
	}
	t.Setenv("ELASTICSEARCH_URL", startFake(t, nil))
}

func (*TestCaseDefaultClient) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	v6, err := elasticsearch6.NewDefaultClient()
	require.NoError(t, err)
	perform(ctx, t, v6, "v6-default-client")

	v7, err := elasticsearch7.NewDefaultClient()
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-default-client")

	v8, err := elasticsearch8.NewDefaultClient()
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-default-client")
}

// ExpectedSpanCount is test.root, then an elasticsearch.query span and its
// net/http span for each request and for the GET / the v7 client sends first.
func (*TestCaseDefaultClient) ExpectedSpanCount() int {
	return 1 + 2*(3+1)
}

func (*TestCaseDefaultClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("v6-default-client"),
				httpSpan("v6-default-client"),
				querySpan("v7-default-client"),
				httpSpan("v7-default-client"),
				querySpan("v8-default-client"),
				httpSpan("v8-default-client"),
			},
		},
	}
}
