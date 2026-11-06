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

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCasePanic checks that a handler that panics runs once and that the panic
// reaches the caller. httputil.ReverseProxy panics with http.ErrAbortHandler to
// abort a response.
type TestCasePanic struct {
	router *httprouter.Router
	calls  int
}

func (tc *TestCasePanic) Setup(context.Context, *testing.T) {
	tc.router = httprouter.New()
	tc.router.GET("/panic/:id", func(http.ResponseWriter, *http.Request, httprouter.Params) {
		tc.calls++
		panic(http.ErrAbortHandler)
	})
}

func (tc *TestCasePanic) Run(ctx context.Context, t *testing.T) {
	recovered := func() (recovered any) {
		defer func() { recovered = recover() }()
		tc.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/panic/42", nil))
		return nil
	}()
	require.Equal(t, http.ErrAbortHandler, recovered)
	require.Equal(t, 1, tc.calls, "handler calls")
}

func (*TestCasePanic) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /panic/:id",
				"service":  "http.router",
			},
			Meta: map[string]string{
				"component": "julienschmidt/httprouter",
				"span.kind": "server",
			},
		},
	}
}
