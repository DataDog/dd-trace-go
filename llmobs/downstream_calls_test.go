// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/llmobstest"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

type downstreamCall struct {
	Kind       string  `json:"kind"`
	Method     string  `json:"method"`
	Host       string  `json:"host"`
	Target     string  `json:"target"`
	Status     string  `json:"status"`
	Count      int     `json:"count"`
	ErrorCount int     `json:"error_count"`
	DurationMs float64 `json:"duration_ms"`
}

type downstreamCalls struct {
	Calls           []downstreamCall `json:"calls"`
	Hosts           []string         `json:"hosts"`
	Targets         []string         `json:"targets"`
	Statuses        []string         `json:"statuses"`
	Endpoints       []string         `json:"endpoints"`
	TotalCount      int              `json:"total_count"`
	TotalErrorCount int              `json:"total_error_count"`
	OmittedCount    int              `json:"omitted_count"`
}

func requireDownstreamCalls(t *testing.T, tt *llmobstest.Collector, name string) *downstreamCalls {
	t.Helper()
	tracer.Flush()
	span := tt.RequireSpan(t, name)
	raw, err := json.Marshal(span.Meta["metadata"])
	require.NoError(t, err)
	var metadata struct {
		DD struct {
			DownstreamCalls *downstreamCalls `json:"downstream_calls"`
		} `json:"_dd"`
	}
	require.NoError(t, json.Unmarshal(raw, &metadata))
	return metadata.DD.DownstreamCalls
}

func httpCall(ctx context.Context, method, url, status string) {
	span, _ := tracer.StartSpanFromContext(ctx, "http.request",
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.Component, "net/http"),
		tracer.Tag(ext.HTTPMethod, method),
		tracer.Tag(ext.HTTPURL, url),
		tracer.Tag(ext.HTTPCode, status),
	)
	span.Finish()
}

func sortedCalls(calls []downstreamCall) []downstreamCall {
	sort.Slice(calls, func(i, j int) bool { return calls[i].Target < calls[j].Target })
	for i := range calls {
		calls[i].DurationMs = 0
	}
	return calls
}

func TestToolSpanDownstreamCalls(t *testing.T) {
	t.Run("http-calls-grouped-by-endpoint", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "execute_code")
		httpCall(ctx, "GET", "https://api.example.com/v1/orders/123", "200")
		httpCall(ctx, "GET", "https://user:pw@api.example.com/v1/orders/456?token=secret", "200")
		httpCall(ctx, "POST", "https://api.example.com/search", "429")
		tool.Finish()

		got := requireDownstreamCalls(t, tt, "execute_code")
		require.NotNil(t, got)
		assert.Equal(t, []downstreamCall{
			{Kind: "http", Method: "POST", Host: "api.example.com", Target: "/search", Status: "429", Count: 1, ErrorCount: 1},
			{Kind: "http", Method: "GET", Host: "api.example.com", Target: "/v1/orders/?", Status: "200", Count: 2},
		}, sortedCalls(got.Calls))
		assert.Equal(t, []string{"api.example.com"}, got.Hosts)
		assert.ElementsMatch(t, []string{"/v1/orders/?", "/search"}, got.Targets)
		assert.ElementsMatch(t, []string{"200", "429"}, got.Statuses)
		assert.ElementsMatch(t, []string{"GET api.example.com/v1/orders/?", "POST api.example.com/search"}, got.Endpoints)
		assert.Equal(t, 3, got.TotalCount)
		assert.Equal(t, 1, got.TotalErrorCount)
		assert.Zero(t, got.OmittedCount)

		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "secret")
		assert.NotContains(t, string(raw), "pw")
	})

	t.Run("db-grpc-and-errors", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "t")
		db, _ := tracer.StartSpanFromContext(ctx, "postgres.query",
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.DBSystem, "postgresql"),
			tracer.Tag(ext.NetworkDestinationName, "db.internal"),
			tracer.ResourceName("SELECT * FROM users WHERE email = 'a@b.c'"),
		)
		db.Finish(tracer.WithError(errors.New("connection reset")))
		rpc, _ := tracer.StartSpanFromContext(ctx, "grpc.client",
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.GRPCFullMethod, "/search.Search/Query"),
		)
		rpc.Finish()
		tool.Finish()

		got := requireDownstreamCalls(t, tt, "t")
		require.NotNil(t, got)
		byKind := map[string]downstreamCall{}
		for _, c := range got.Calls {
			byKind[c.Kind] = c
		}
		assert.Equal(t, "postgresql", byKind["db"].Method)
		assert.Equal(t, "db.internal", byKind["db"].Host)
		assert.Equal(t, "postgres.query", byKind["db"].Target)
		assert.Equal(t, 1, byKind["db"].ErrorCount)
		assert.Equal(t, "/search.Search/Query", byKind["grpc"].Target)
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "SELECT")
	})

	t.Run("nested-client-span-counted-once", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "t")
		outer, outerCtx := tracer.StartSpanFromContext(ctx, "retrying.client",
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.HTTPMethod, "GET"),
			tracer.Tag(ext.HTTPURL, "https://api.example.com/v1/orders/1"),
			tracer.Tag(ext.HTTPCode, "200"),
		)
		httpCall(outerCtx, "GET", "https://api.example.com/v1/orders/1", "200")
		outer.Finish()
		tool.Finish()

		got := requireDownstreamCalls(t, tt, "t")
		require.NotNil(t, got)
		assert.Equal(t, 1, got.TotalCount)
	})

	t.Run("concurrent-goroutines", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "t")
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Go(func() {
				httpCall(ctx, "GET", fmt.Sprintf("https://api.example.com/v1/orders/%d", i), "200")
			})
		}
		wg.Wait()
		tool.Finish()

		got := requireDownstreamCalls(t, tt, "t")
		require.NotNil(t, got)
		require.Len(t, got.Calls, 1)
		assert.Equal(t, 20, got.Calls[0].Count)
	})

	t.Run("only-tool-spans-report", func(t *testing.T) {
		tt := testTracer(t)
		wf, wfCtx := llmobs.StartWorkflowSpan(context.Background(), "wf")
		httpCall(wfCtx, "GET", "https://api.example.com/a", "200")
		tool, toolCtx := llmobs.StartToolSpan(wfCtx, "t")
		llm, llmCtx := llmobs.StartLLMSpan(toolCtx, "llm")
		httpCall(llmCtx, "POST", "https://api.openai.com/v1/responses", "200")
		llm.Finish()
		tool.Finish()
		wf.Finish()

		assert.Nil(t, requireDownstreamCalls(t, tt, "wf"))
		assert.Nil(t, requireDownstreamCalls(t, tt, "llm"))
		assert.Nil(t, requireDownstreamCalls(t, tt, "t"), "calls inside an LLM span stay with that span")
	})

	t.Run("non-client-and-late-spans-ignored", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "t")
		internal, _ := tracer.StartSpanFromContext(ctx, "internal.work")
		internal.Finish()
		late, _ := tracer.StartSpanFromContext(ctx, "http.request",
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.HTTPURL, "https://api.example.com/late"),
		)
		tool.Finish()
		late.Finish()

		assert.Nil(t, requireDownstreamCalls(t, tt, "t"))
	})

	t.Run("distinct-endpoints-capped", func(t *testing.T) {
		tt := testTracer(t)
		tool, ctx := llmobs.StartToolSpan(context.Background(), "t")
		for i := range 55 {
			httpCall(ctx, "GET", fmt.Sprintf("https://api.example.com/path-%d", i), "200")
		}
		tool.Finish()

		got := requireDownstreamCalls(t, tt, "t")
		require.NotNil(t, got)
		assert.Len(t, got.Calls, 50)
		assert.Equal(t, 5, got.OmittedCount)
		assert.Equal(t, 55, got.TotalCount, "totals include omitted calls")
	})
}
