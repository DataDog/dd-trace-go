// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapidisabled

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase checks that the OpenTelemetry API bridge does nothing when DD_TRACE_OTEL_ENABLED is
// not set. The OpenTelemetry API does not see the Datadog server span, and the OpenTelemetry
// global tracer does not create Datadog spans.
type TestCase struct {
	srv             *http.Server
	serverSpanValid atomic.Bool
}

// PreBootstrap pins the flag off, so the suite cannot fail because some other
// mechanism turns the bridge on.
func (*TestCase) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_OTEL_ENABLED", "false")
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}

	ln := net.FreeListener(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", tc.handle)
	tc.srv = &http.Server{
		Addr:         ln.Addr().String(),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() { assert.ErrorIs(t, tc.srv.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Use a new context, because the original context may have expired during cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.srv.Shutdown(ctx))
	})
}

func (tc *TestCase) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tc.serverSpanValid.Store(oteltrace.SpanFromContext(ctx).SpanContext().IsValid())

	_, child := otel.Tracer("otel_api_disabled").Start(ctx, "otel.child")
	child.End()

	w.WriteHeader(http.StatusOK)
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/hello", tc.srv.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.False(t, tc.serverSpanValid.Load())
}

// ExpectedSpanCount is 2: the client span and the server span. The bridge must not create a
// Datadog span for "otel.child".
func (*TestCase) ExpectedSpanCount() int { return 2 }

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /hello",
				"type":     "http",
			},
			Meta: map[string]string{
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /hello",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "server",
					},
				},
			},
		},
	}
}
