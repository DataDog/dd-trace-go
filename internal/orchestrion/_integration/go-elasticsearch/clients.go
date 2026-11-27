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
)

// TestCaseClients checks that each go-elasticsearch major traces a client
// built from a Config value, from a dereferenced *Config, and, in v8, with
// NewTypedClient, and that a custom Transport is wrapped rather than replaced.
// Each request reports exactly one elasticsearch.query span. The fake node
// runs no net/http server, so the only other spans are the net/http client
// spans of the round trips.
type TestCaseClients struct {
	addr      string
	transport countingTransport
}

func (tc *TestCaseClients) Setup(_ context.Context, t *testing.T) {
	tc.addr = startFake(t, nil)
}

func (tc *TestCaseClients) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	addrs := []string{tc.addr}

	v6, err := elasticsearch6.NewClient(elasticsearch6.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v6, "v6-new-client")
	v6Cfg := &elasticsearch6.Config{Addresses: addrs}
	v6, err = elasticsearch6.NewClient(*v6Cfg)
	require.NoError(t, err)
	perform(ctx, t, v6, "v6-new-client-pointer")

	v7, err := elasticsearch7.NewClient(elasticsearch7.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-new-client")
	v7Cfg := &elasticsearch7.Config{Addresses: addrs}
	v7, err = elasticsearch7.NewClient(*v7Cfg)
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-new-client-pointer")

	v8, err := elasticsearch8.NewClient(elasticsearch8.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-new-client")
	v8Cfg := &elasticsearch8.Config{Addresses: addrs}
	v8, err = elasticsearch8.NewClient(*v8Cfg)
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-new-client-pointer")
	typed, err := elasticsearch8.NewTypedClient(elasticsearch8.Config{Addresses: addrs})
	require.NoError(t, err)
	perform(ctx, t, typed, "v8-new-typed-client")
	v8, err = elasticsearch8.NewClient(elasticsearch8.Config{Addresses: addrs, Transport: &tc.transport})
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-custom-transport")
	require.EqualValues(t, 1, tc.transport.calls.Load())
}

// ExpectedSpanCount is test.root, then an elasticsearch.query span and its
// net/http span for each of the 8 requests and for the GET / that each of the
// two v7 clients sends first.
func (*TestCaseClients) ExpectedSpanCount() int {
	return 1 + 2*(8+2)
}

func (*TestCaseClients) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("v6-new-client"),
				httpSpan("v6-new-client"),
				querySpan("v6-new-client-pointer"),
				httpSpan("v6-new-client-pointer"),
				querySpan("v7-new-client"),
				httpSpan("v7-new-client"),
				querySpan("v7-new-client-pointer"),
				httpSpan("v7-new-client-pointer"),
				querySpan("v8-new-client"),
				httpSpan("v8-new-client"),
				querySpan("v8-new-client-pointer"),
				httpSpan("v8-new-client-pointer"),
				querySpan("v8-new-typed-client"),
				httpSpan("v8-new-typed-client"),
				querySpan("v8-custom-transport"),
				httpSpan("v8-custom-transport"),
			},
		},
	}
}
