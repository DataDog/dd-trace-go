// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseContribTransport checks that a client whose Transport the
// application already set up with the contrib is traced once, with the
// contrib's options.
type TestCaseContribTransport struct {
	addr string
}

func (tc *TestCaseContribTransport) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion wraps the contrib's transport again and reports two spans per request")
	}
	tc.addr = startFake(t, nil)
}

func (tc *TestCaseContribTransport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	v7, err := elasticsearch7.NewClient(elasticsearch7.Config{
		Addresses: []string{tc.addr},
		Transport: elastictrace.NewRoundTripper(elastictrace.WithService("contrib.client")),
	})
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-contrib-transport")

	v8, err := elasticsearch8.NewClient(elasticsearch8.Config{
		Addresses: []string{tc.addr},
		Transport: elastictrace.NewRoundTripper(elastictrace.WithService("contrib.client")),
	})
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-contrib-transport")
}

// ExpectedSpanCount is test.root, then an elasticsearch.query span and its
// net/http span for each request and for the GET / the v7 client sends first.
func (*TestCaseContribTransport) ExpectedSpanCount() int {
	return 1 + 2*(2+1)
}

func (*TestCaseContribTransport) ExpectedTraces() trace.Traces {
	v7 := querySpan("v7-contrib-transport")
	v7.Tags["service"] = "contrib.client"
	v8 := querySpan("v8-contrib-transport")
	v8.Tags["service"] = "contrib.client"
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{v7, httpSpan("v7-contrib-transport"), v8, httpSpan("v8-contrib-transport")},
		},
	}
}
