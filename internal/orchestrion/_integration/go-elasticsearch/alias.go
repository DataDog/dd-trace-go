// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	es6 "github.com/elastic/go-elasticsearch/v6"
	es7 "github.com/elastic/go-elasticsearch/v7"
	es8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that clients are traced when go-elasticsearch is
// imported under names other than the ones the rules use.
type TestCaseAliasedImport struct {
	addr string
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.addr = startFake(t, nil)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	addrs := []string{tc.addr}

	v6, err := es6.NewClient(es6.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v6, "alias-v6-new-client")

	v7, err := es7.NewClient(es7.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v7, "alias-v7-new-client")

	v8, err := es8.NewClient(es8.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v8, "alias-v8-new-client")
}

// ExpectedSpanCount is test.root, then an elasticsearch.query span and its
// net/http span for each request and for the GET / the v7 client sends first.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 1 + 2*(3+1)
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("alias-v6-new-client"),
				httpSpan("alias-v6-new-client"),
				querySpan("alias-v7-new-client"),
				httpSpan("alias-v7-new-client"),
				querySpan("alias-v8-new-client"),
				httpSpan("alias-v8-new-client"),
			},
		},
	}
}
