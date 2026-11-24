// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package julienschmidt_httprouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"

	httptrace "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseContribRouter checks that a router the application wrapped with the
// contrib itself gets one httprouter span, not one from the contrib and one
// from the rules. Orchestrion still reports two, so it only runs under otelc.
type TestCaseContribRouter struct {
	handler http.Handler
}

func (tc *TestCaseContribRouter) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion reports two httprouter spans for a router wrapped with the contrib")
	}
	router := httptrace.New()
	router.GET("/contrib/:id", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.WriteHeader(http.StatusOK)
	})
	tc.handler = router
}

func (tc *TestCaseContribRouter) Run(ctx context.Context, t *testing.T) {
	rec := httptest.NewRecorder()
	tc.handler.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/contrib/42", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func (*TestCaseContribRouter) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseContribRouter) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /contrib/:id",
				"type":     "web",
				"service":  "http.router",
			},
			Meta: map[string]string{
				"component":  "julienschmidt/httprouter",
				"span.kind":  "server",
				"http.route": "/contrib/:id",
			},
		},
	}
}
