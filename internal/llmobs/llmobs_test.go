// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

const (
	mlApp = "gotest"
)

func TestStartSpan(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ll, err := llmobs.ActiveLLMObs()
	require.NoError(t, err)

	t.Run("simple", func(t *testing.T) {
		ctx := context.Background()
		span, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})

		apmSpans := tt.WaitForSpans(t, 1)
		s0 := apmSpans[0]
		assert.Equal(t, "llm-1", s0.Name)

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]
		assert.Equal(t, "llm-1", l0.Name)
	})

	t.Run("child-spans", func(t *testing.T) {
		ctx := context.Background()
		ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{})
		ss1, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1", llmobs.StartSpanConfig{})
		ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
		ss3, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-2", llmobs.StartSpanConfig{})

		ss3.Finish(llmobs.FinishSpanConfig{})
		ss2.Finish()
		ss1.Finish(llmobs.FinishSpanConfig{})
		ss0.Finish(llmobs.FinishSpanConfig{})

		apmSpans := tt.WaitForSpans(t, 4)

		s0 := apmSpans[0]
		s1 := apmSpans[1]
		s2 := apmSpans[2]
		s3 := apmSpans[3]

		assert.Equal(t, "llm-1", s0.Name)
		assert.Equal(t, "agent-1", s1.Name)
		assert.Equal(t, "apm-1", s2.Name)
		assert.Equal(t, "llm-2", s3.Name)

		apmTraceID := s0.TraceID
		assert.Equal(t, apmTraceID, s1.TraceID)
		assert.Equal(t, apmTraceID, s2.TraceID)
		assert.Equal(t, apmTraceID, s3.TraceID)

		llmSpans := tt.WaitForLLMObsSpans(t, 3)

		l0 := llmSpans[0]
		l1 := llmSpans[1]
		l2 := llmSpans[2]

		// FIXME: they are in reverse order
		assert.Equal(t, "llm-2", l0.Name)
		assert.Equal(t, "agent-1", l1.Name)
		assert.Equal(t, "llm-1", l2.Name)

		llmobsTraceID := l0.TraceID
		assert.Equal(t, llmobsTraceID, l1.TraceID)
		assert.Equal(t, llmobsTraceID, l2.TraceID)
	})

	t.Run("distributed-context-propagation", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			ss3, ctx := tracer.StartSpanFromContext(ctx, "apm-2")
			defer ss3.Finish()

			ss4, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1", llmobs.StartSpanConfig{})
			defer ss4.Finish(llmobs.FinishSpanConfig{})

			w.Write([]byte("ok"))
		})
		srv, cl := testClientServer(t, h)

		genSpans := func() {
			ctx := context.Background()
			ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{MLApp: "custom-ml-app"})
			defer ss0.Finish(llmobs.FinishSpanConfig{})

			ss1, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "workflow-1", llmobs.StartSpanConfig{})
			defer ss1.Finish(llmobs.FinishSpanConfig{})

			ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
			defer ss2.Finish()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
			require.NoError(t, err)
			resp, err := cl.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()
		}

		genSpans()
		apmSpans := tt.WaitForSpans(t, 7)

		httpServer := apmSpans[0]
		apm2 := apmSpans[1]
		agent1 := apmSpans[2]
		llm1 := apmSpans[3]
		workflow1 := apmSpans[4]
		apm1 := apmSpans[5]
		httpClient := apmSpans[6]

		assert.Equal(t, "http.request", httpServer.Name)
		assert.Equal(t, "server", httpServer.Meta["span.kind"])
		assert.Equal(t, "apm-2", apm2.Name)
		assert.Equal(t, "agent-1", agent1.Name)
		assert.Equal(t, "llm-1", llm1.Name)
		assert.Equal(t, "workflow-1", workflow1.Name)
		assert.Equal(t, "apm-1", apm1.Name)
		assert.Equal(t, "http.request", httpClient.Name)
		assert.Equal(t, "client", httpClient.Meta["span.kind"])

		apmTraceID := httpServer.TraceID
		assert.Equal(t, apmTraceID, apm2.TraceID, "wrong trace ID for span apm-2")
		assert.Equal(t, apmTraceID, agent1.TraceID, "wrong trace ID for span agent-1")
		assert.Equal(t, apmTraceID, llm1.TraceID, "wrong trace ID for span llm-1")
		assert.Equal(t, apmTraceID, workflow1.TraceID, "wrong trace ID for span workflow-1")
		assert.Equal(t, apmTraceID, apm1.TraceID, "wrong trace ID for span apm-1")
		assert.Equal(t, apmTraceID, httpClient.TraceID, "wrong trace ID for span http-client")

		// check correct span linkage
		assert.Equal(t, httpClient.SpanID, httpServer.ParentID)
		assert.Equal(t, httpServer.SpanID, apm2.ParentID)
		assert.Equal(t, apm2.SpanID, agent1.ParentID)

		assert.Equal(t, apm1.SpanID, httpClient.ParentID)
		assert.Equal(t, llm1.SpanID, workflow1.ParentID)
		assert.Equal(t, workflow1.SpanID, apm1.ParentID)
		assert.Equal(t, uint64(0), llm1.ParentID)

		llmSpans := tt.WaitForLLMObsSpans(t, 3)

		l0 := llmSpans[0]
		l1 := llmSpans[1]
		l2 := llmSpans[2]

		assert.Equal(t, "agent-1", l0.Name)
		assert.Equal(t, "custom-ml-app", findTag(l0.Tags, "ml_app"), "wrong ml_app for span agent-1")
		assert.Equal(t, "workflow-1", l1.Name)
		assert.Equal(t, "custom-ml-app", findTag(l1.Tags, "ml_app"), "wrong ml_app for span workflow-1")
		assert.Equal(t, "llm-1", l2.Name)
		assert.Equal(t, "custom-ml-app", findTag(l2.Tags, "ml_app"), "wrong ml_app for span llm-1")

		llmTraceID := l0.TraceID
		assert.Equal(t, llmTraceID, l0.TraceID)
		assert.Equal(t, llmTraceID, l1.TraceID)
	})
}

func BenchmarkStartSpan(b *testing.B) {
	run := func(b *testing.B, ll *llmobs.LLMObs, tt *testtracer.TestTracer, done chan struct{}) {
		llmSpans := make([]testtracer.LLMObsSpan, 0, b.N)
		apmSpans := make([]testtracer.Span, 0, b.N)

		go func(n int) {
			llCnt, apmCnt := 0, 0
			for llCnt < n || apmCnt < n {
				select {
				case llmSpan := <-tt.LLMSpans:
					llmSpans = append(llmSpans, llmSpan)
					llCnt++
				case apmSpan := <-tt.Spans:
					apmSpans = append(apmSpans, apmSpan)
					apmCnt++
				}
			}
			close(done)
		}(b.N)

		b.Log("starting benchmark")

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			span, _ := ll.StartSpan(context.Background(), llmobs.SpanKindLLM, fmt.Sprintf("span-%d", i), llmobs.StartSpanConfig{})
			span.Finish(llmobs.FinishSpanConfig{})
		}
		b.StopTimer()

		b.Log("finished benchmark")

		b.Log("waiting for spans")

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			b.Logf("timeout waiting for spans: got llm=%d apm=%d want=%d",
				len(llmSpans), len(apmSpans), b.N)
		}
	}

	b.Run("basic", func(b *testing.B) {
		tt := testTracer(b, testtracer.WithRequestDelay(500*time.Millisecond))
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(b, err)

		done := make(chan struct{})

		run(b, ll, tt, done)
	})
	b.Run("periodic-flush", func(b *testing.B) {
		tt := testTracer(b, testtracer.WithRequestDelay(500*time.Millisecond))
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(b, err)

		ticker := time.NewTicker(10 * time.Microsecond)
		defer ticker.Stop()

		done := make(chan struct{})

		// force flushes to test if StartSpan gets blocked while the tracer is sending payloads
		go func() {
			for {
				select {
				case <-ticker.C:
					ll.Flush()

				case <-done:
					return
				}
			}
		}()

		run(b, ll, tt, done)
	})
}

func testTracer(t testing.TB, opts ...testtracer.Option) *testtracer.TestTracer {
	tOpts := append([]testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp(mlApp),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	}, opts...)

	return testtracer.Start(t, tOpts...)
}

func testClientServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	wh := traceHandler(h)
	srv := httptest.NewServer(wh)
	cl := traceClient(srv.Client())
	t.Cleanup(srv.Close)

	return srv, cl
}

func traceHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		opts := []tracer.StartSpanOption{
			tracer.Tag("span.kind", "server"),
		}
		parentCtx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Header))
		if err == nil && parentCtx != nil {
			opts = append(opts, tracer.ChildOf(parentCtx))
		}

		span, ctx := tracer.StartSpanFromContext(ctx, "http.request", opts...)
		defer span.Finish()

		h.ServeHTTP(w, req.WithContext(ctx))
	})
}

func traceClient(c *http.Client) *http.Client {
	c.Transport = &tracedRT{base: c.Transport}
	return c
}

type tracedRT struct {
	base http.RoundTripper
}

func (rt *tracedRT) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	span, ctx := tracer.StartSpanFromContext(ctx, "http.request", tracer.Tag("span.kind", "client"))
	defer span.Finish()

	// Clone the request so we can modify it without causing visible side-effects to the caller...
	req = req.Clone(ctx)
	err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header))
	if err != nil {
		fmt.Fprintf(os.Stderr, "contrib/net/http.Roundtrip: failed to inject http headers: %s\n", err.Error())
	}

	return rt.base.RoundTrip(req)
}

func findTag(tags []string, name string) string {
	for _, t := range tags {
		parts := strings.Split(t, ":")
		if len(parts) != 2 {
			continue
		}
		if parts[0] == name {
			return parts[1]
		}
	}
	return ""
}
