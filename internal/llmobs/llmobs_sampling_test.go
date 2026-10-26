// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

func TestSampling(t *testing.T) {
	t.Run("default-rate-keeps-every-trace", func(t *testing.T) {
		_, coll, ll := testTracer(t)

		root, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root", llmobs.StartSpanConfig{})
		child, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})
		child.Finish(llmobs.FinishSpanConfig{})
		root.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		for _, name := range []string{"root", "child"} {
			s := coll.RequireSpan(t, name)
			assert.Equal(t, "1", s.DDAttributes.SampleRate, name)
			assert.Equal(t, llmobs.SamplingDecisionKeep, s.DDAttributes.SamplingDecision, name)
		}
	})

	t.Run("rate-zero-still-submits-every-span", func(t *testing.T) {
		// Dropped traces are still sent so the backend can compute token and cost metrics
		// over all traffic; it discards their content.
		_, coll, ll := testTracer(t, tracer.WithLLMObsSampleRate(0))

		root, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindAgent, "root", llmobs.StartSpanConfig{})
		child, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})
		child.Finish(llmobs.FinishSpanConfig{})
		root.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		for _, name := range []string{"root", "child"} {
			s := coll.RequireSpan(t, name)
			assert.Equal(t, "0", s.DDAttributes.SampleRate, name)
			assert.Equal(t, llmobs.SamplingDecisionDrop, s.DDAttributes.SamplingDecision, name)
		}
	})

	t.Run("env-var", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_SAMPLE_RATE", "0")
		_, coll, ll := testTracer(t)

		span, _ := ll.StartSpan(context.Background(), llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		s := coll.RequireSpan(t, "span")
		assert.Equal(t, "0", s.DDAttributes.SampleRate)
		assert.Equal(t, llmobs.SamplingDecisionDrop, s.DDAttributes.SamplingDecision)
	})

	t.Run("descendants-inherit-the-root-decision", func(t *testing.T) {
		_, coll, ll := testTracer(t, tracer.WithLLMObsSampleRate(0.5))

		const traces = 50
		for i := range traces {
			root, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindWorkflow, "root-"+strconv.Itoa(i), llmobs.StartSpanConfig{})
			mid, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "mid-"+strconv.Itoa(i), llmobs.StartSpanConfig{})
			leaf, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "leaf-"+strconv.Itoa(i), llmobs.StartSpanConfig{})
			leaf.Finish(llmobs.FinishSpanConfig{})
			mid.Finish(llmobs.FinishSpanConfig{})
			root.Finish(llmobs.FinishSpanConfig{})
		}
		tracer.Flush()

		seen := map[string]bool{}
		for i := range traces {
			root := coll.RequireSpan(t, "root-"+strconv.Itoa(i))
			assert.Equal(t, "0.5", root.DDAttributes.SampleRate)
			for _, name := range []string{"mid-", "leaf-"} {
				s := coll.RequireSpan(t, name+strconv.Itoa(i))
				assert.Equal(t, root.DDAttributes.SampleRate, s.DDAttributes.SampleRate, s.Name)
				assert.Equal(t, root.DDAttributes.SamplingDecision, s.DDAttributes.SamplingDecision, s.Name)
			}
			seen[root.DDAttributes.SamplingDecision] = true
		}
		assert.True(t, seen[llmobs.SamplingDecisionKeep], "expected some kept traces at rate 0.5")
		assert.True(t, seen[llmobs.SamplingDecisionDrop], "expected some dropped traces at rate 0.5")
	})

	t.Run("propagated-decision-is-honored", func(t *testing.T) {
		_, coll, ll := testTracer(t)

		ctx := llmobs.ContextWithPropagatedLLMSpan(context.Background(), &llmobs.PropagatedLLMSpan{
			TraceID:          "propagated-trace",
			SpanID:           "propagated-span",
			SampleRate:       "0.25",
			SamplingDecision: llmobs.SamplingDecisionDrop,
		})
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		// The upstream root's decision wins over the local rate of 1.
		s := coll.RequireSpan(t, "span")
		assert.Equal(t, "0.25", s.DDAttributes.SampleRate)
		assert.Equal(t, llmobs.SamplingDecisionDrop, s.DDAttributes.SamplingDecision)
	})

	t.Run("propagated-without-decision-stays-undecided", func(t *testing.T) {
		// An upstream SDK without sampling support sends no decision. Deciding here could
		// split the trace, so the span ships undecided like the rest of the trace.
		_, coll, ll := testTracer(t, tracer.WithLLMObsSampleRate(0))

		ctx := llmobs.ContextWithPropagatedLLMSpan(context.Background(), &llmobs.PropagatedLLMSpan{
			TraceID: "propagated-trace",
			SpanID:  "propagated-span",
		})
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		s := coll.RequireSpan(t, "span")
		assert.Empty(t, s.DDAttributes.SampleRate)
		assert.Empty(t, s.DDAttributes.SamplingDecision)
	})

	t.Run("distributed-keep-at-rate-one-is-not-propagated", func(t *testing.T) {
		// The default decision is what downstream spans assume without one, so it stays off
		// the wire to leave tracestate room for the parent-agent name.
		_, coll, ll := testTracer(t)

		var datadogTags string
		h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			datadogTags = req.Header.Get("x-datadog-tags")
			server, _ := ll.StartSpan(req.Context(), llmobs.SpanKindTool, "server-tool", llmobs.StartSpanConfig{})
			server.Finish(llmobs.FinishSpanConfig{})
			w.Write([]byte("ok"))
		})
		srv := httptest.NewServer(traceHandler(h))
		defer srv.Close()

		client, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindAgent, "client_agent", llmobs.StartSpanConfig{})
		apmSpan, ctx := tracer.StartSpanFromContext(ctx, "http.client.request")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		require.NoError(t, tracer.Inject(apmSpan.Context(), tracer.HTTPHeadersCarrier(req.Header)))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		apmSpan.Finish()
		client.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		assert.NotContains(t, datadogTags, "_dd.p.llmobs_sr=")
		assert.NotContains(t, datadogTags, "_dd.p.llmobs_sd=")
		assert.Contains(t, datadogTags, "_dd.p.llmobs_pagent_name=client_agent")

		assert.Equal(t, llmobs.SamplingDecisionKeep, coll.RequireSpan(t, "client_agent").DDAttributes.SamplingDecision)
		assert.Empty(t, coll.RequireSpan(t, "server-tool").DDAttributes.SamplingDecision)
	})

	t.Run("distributed-trace-carries-the-decision", func(t *testing.T) {
		_, coll, ll := testTracer(t, tracer.WithLLMObsSampleRate(0))

		var datadogTags string
		h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			datadogTags = req.Header.Get("x-datadog-tags")
			server, _ := ll.StartSpan(req.Context(), llmobs.SpanKindAgent, "server-agent", llmobs.StartSpanConfig{})
			server.Finish(llmobs.FinishSpanConfig{})
			w.Write([]byte("ok"))
		})
		srv := httptest.NewServer(traceHandler(h))
		defer srv.Close()

		client, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindAgent, "client-agent", llmobs.StartSpanConfig{})
		apmSpan, ctx := tracer.StartSpanFromContext(ctx, "http.client.request")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		require.NoError(t, tracer.Inject(apmSpan.Context(), tracer.HTTPHeadersCarrier(req.Header)))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		apmSpan.Finish()
		client.Finish(llmobs.FinishSpanConfig{})
		tracer.Flush()

		assert.Contains(t, datadogTags, "_dd.p.llmobs_sr=0")
		assert.Contains(t, datadogTags, "_dd.p.llmobs_sd=0")

		clientSpan := coll.RequireSpan(t, "client-agent")
		serverSpan := coll.RequireSpan(t, "server-agent")
		assert.Equal(t, clientSpan.SpanID, serverSpan.ParentID, "server span should continue the client's LLMObs trace")
		assert.Equal(t, "0", serverSpan.DDAttributes.SampleRate)
		assert.Equal(t, llmobs.SamplingDecisionDrop, serverSpan.DDAttributes.SamplingDecision)
	})
}
