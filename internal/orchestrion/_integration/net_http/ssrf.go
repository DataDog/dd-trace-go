// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package nethttp

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSSRF checks that RASP blocks an outbound request built from user
// input. The client instrumentation must skip the real round trip and hand the
// blocking error back with a nil response.
type TestCaseSSRF struct {
	*http.Server
	*testing.T
}

func (tc *TestCaseSSRF) PreBootstrap(_ context.Context, t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("appsec does not support Windows")
		return
	}
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
		return
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/rasp-only-rules.json")
	t.Setenv("DD_APPSEC_RASP_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1h")
}

func (tc *TestCaseSSRF) Setup(_ context.Context, t *testing.T) {
	ln := net.FreeListener(t)
	tc.Server = &http.Server{
		Addr:    ln.Addr().String(),
		Handler: http.HandlerFunc(tc.handleRoot),
	}

	go func() { assert.ErrorIs(t, tc.Server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseSSRF) Run(_ context.Context, t *testing.T) {
	tc.T = t
	resp, err := http.Get(fmt.Sprintf("http://%s/?url=169.254.169.254", tc.Server.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func (*TestCaseSSRF) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /",
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
						"resource": "GET /",
						"type":     "web",
					},
					Meta: map[string]string{
						"component":         "net/http",
						"span.kind":         "server",
						"appsec.blocked":    "true",
						"is.security.error": "true",
					},
				},
			},
		},
	}
}

func (tc *TestCaseSSRF) handleRoot(_ http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://"+r.URL.Query().Get("url"), nil)
	if !assert.NoError(tc.T, err) {
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	assert.Nil(tc.T, resp, "a blocked request must not return a response")
	assert.ErrorIs(tc.T, err, &events.BlockingSecurityEvent{})
	if events.IsSecurityError(err) { // TODO: response writer instrumentation do not have to do that
		span, _ := tracer.SpanFromContext(r.Context())
		span.SetTag("is.security.error", true)
	}
}
