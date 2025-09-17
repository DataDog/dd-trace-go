package llmobs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		span, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1")
		span.Finish()

		apmSpans := tt.WaitForSpans(t, 1)
		s0 := apmSpans[0]
		assert.Equal(t, "llm-1", s0.Name)

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]
		assert.Equal(t, "llm-1", l0.Name)
	})

	t.Run("child-spans", func(t *testing.T) {
		ctx := context.Background()
		ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1")
		ss1, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1")
		ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
		ss3, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-2")

		ss3.Finish()
		ss2.Finish()
		ss1.Finish()
		ss0.Finish()

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
			ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-2")
			defer ss2.Finish()

			ss3, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1")
			defer ss3.Finish()

			w.Write([]byte("ok"))
		})
		srv, cl := testClientServer(t, h)

		genSpans := func() {
			ctx := context.Background()
			ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.WithMLApp("custom-ml-app"))
			defer ss0.Finish()

			ss1, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
			defer ss1.Finish()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
			require.NoError(t, err)
			resp, err := cl.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()
		}

		genSpans()
		apmSpans := tt.WaitForSpans(t, 6)

		t.Logf("apm spans: %+v", apmSpans)

		httpServer := apmSpans[0]
		apm2 := apmSpans[1]
		agent1 := apmSpans[2]
		llm1 := apmSpans[3]
		apm1 := apmSpans[4]
		httpClient := apmSpans[5]

		assert.Equal(t, "http.request", httpServer.Name)
		assert.Equal(t, "server", httpServer.Meta["span.kind"])
		assert.Equal(t, "apm-2", apm2.Name)
		assert.Equal(t, "agent-1", agent1.Name)
		assert.Equal(t, "llm-1", llm1.Name)
		assert.Equal(t, "apm-1", apm1.Name)
		assert.Equal(t, "http.request", httpClient.Name)
		assert.Equal(t, "client", httpClient.Meta["span.kind"])

		apmTraceID := httpServer.TraceID
		assert.Equal(t, apmTraceID, apm2.TraceID, "wrong trace ID for span apm-2")
		assert.Equal(t, apmTraceID, agent1.TraceID, "wrong trace ID for span agent-1")
		assert.Equal(t, apmTraceID, llm1.TraceID, "wrong trace ID for span llm-1")
		assert.Equal(t, apmTraceID, apm1.TraceID, "wrong trace ID for span apm-1")
		assert.Equal(t, apmTraceID, httpClient.TraceID, "wrong trace ID for span http-client")

		// check correct span linkage
		assert.Equal(t, httpClient.SpanID, httpServer.ParentID)
		assert.Equal(t, httpServer.SpanID, apm2.ParentID)
		assert.Equal(t, apm2.SpanID, agent1.ParentID)

		assert.Equal(t, apm1.SpanID, httpClient.ParentID)
		assert.Equal(t, llm1.SpanID, apm1.ParentID)
		assert.Equal(t, uint64(0), llm1.ParentID)

		llmSpans := tt.WaitForLLMObsSpans(t, 2)

		t.Logf("llmobs spans: %+v", llmSpans)

		l0 := llmSpans[0]
		l1 := llmSpans[1]

		assert.Equal(t, "agent-1", l0.Name)
		assert.Equal(t, "custom-ml-app", findTag(l0.Tags, "ml_app"), "wrong ml_app for span agent-1")
		assert.Equal(t, "llm-1", l1.Name)
		assert.Equal(t, "custom-ml-app", findTag(l1.Tags, "ml_app"), "wrong ml_app for span llm-1")

		llmTraceID := l0.TraceID
		assert.Equal(t, llmTraceID, l0.TraceID)
		assert.Equal(t, llmTraceID, l1.TraceID)
	})
}

func testTracer(t *testing.T) *testtracer.TestTracer {
	return testtracer.Start(t,
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp(mlApp),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	)
}

func testClientServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	wh := httptrace.WrapHandler(h, mlApp, "GET /")
	srv := httptest.NewServer(wh)
	cl := httptrace.WrapClient(srv.Client())
	t.Cleanup(srv.Close)

	return srv, cl
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
