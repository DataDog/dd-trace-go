// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapi

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
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase checks that the OpenTelemetry API sees the net/http server span and that the
// OpenTelemetry global tracer creates child spans with the Datadog tracer.
type TestCase struct {
	bridgeEnabled
	srv             *http.Server
	serverSpanValid atomic.Bool
	childSpanValid  atomic.Bool
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
	span := oteltrace.SpanFromContext(ctx)
	tc.serverSpanValid.Store(span.SpanContext().IsValid())
	span.SetAttributes(attribute.String("otel.server.attribute", "value"))

	_, child := otel.Tracer("otel_api").Start(ctx, "otel.child")
	tc.childSpanValid.Store(child.SpanContext().IsValid())
	child.SetAttributes(attribute.String("otel.child.attribute", "value"))
	child.End()

	w.WriteHeader(http.StatusOK)
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/hello", tc.srv.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.True(t, tc.serverSpanValid.Load())
	require.True(t, tc.childSpanValid.Load())
}

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
						"component":             "net/http",
						"span.kind":             "server",
						"otel.server.attribute": "value",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "internal",
								"resource": "otel.child",
							},
							Meta: map[string]string{
								"span.kind":            "internal",
								"otel.child.attribute": "value",
							},
						},
					},
				},
			},
		},
	}
}
