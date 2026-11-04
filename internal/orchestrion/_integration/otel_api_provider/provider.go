// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapiprovider

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type recordingProvider struct {
	noop.TracerProvider
	tracer *recordingTracer
}

func (p *recordingProvider) Tracer(string, ...oteltrace.TracerOption) oteltrace.Tracer {
	return p.tracer
}

type recordingTracer struct {
	noop.Tracer
	mu      sync.Mutex
	names   []string
	parents []oteltrace.SpanContext
}

func (rt *recordingTracer) Start(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	rt.mu.Lock()
	rt.names = append(rt.names, name)
	rt.parents = append(rt.parents, oteltrace.SpanContextFromContext(ctx))
	rt.mu.Unlock()
	return rt.Tracer.Start(ctx, name, opts...)
}

// TestCase checks that a TracerProvider that the application sets wins over the Datadog
// bridge, and that the spans of this provider see the Datadog span as their parent.
// This test has its own package, because otel.SetTracerProvider changes process-wide state.
type TestCase struct {
	bridgeEnabled
	srv          *http.Server
	tracer       *recordingTracer
	early        oteltrace.Tracer
	serverSpanID atomic.Value // oteltrace.SpanID
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}

	tc.tracer = &recordingTracer{}
	// A tracer that the application got before SetTracerProvider must also use the provider of
	// the application.
	tc.early = otel.Tracer("otel_api_provider.early")
	otel.SetTracerProvider(&recordingProvider{tracer: tc.tracer})

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
	tc.serverSpanID.Store(oteltrace.SpanFromContext(ctx).SpanContext().SpanID())

	_, child := tc.early.Start(ctx, "user.child")
	child.End()

	w.WriteHeader(http.StatusOK)
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/hello", tc.srv.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	tc.tracer.mu.Lock()
	defer tc.tracer.mu.Unlock()
	require.Equal(t, []string{"user.child"}, tc.tracer.names)
	require.True(t, tc.tracer.parents[0].IsValid())
	serverSpanID, ok := tc.serverSpanID.Load().(oteltrace.SpanID)
	require.True(t, ok)
	require.Equal(t, serverSpanID, tc.tracer.parents[0].SpanID())
}

// ExpectedSpanCount is 2: the client span and the server span. The bridge must not create a
// Datadog span for "user.child".
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
