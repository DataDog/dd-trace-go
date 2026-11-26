// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package os

import (
	"context"
	"fmt"
	"net/http"
	"os"
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

// TestCaseRoot opens the file through an [os.Root], whose OpenFile does not
// call [os.OpenFile].
type TestCaseRoot struct {
	*http.Server
	*testing.T
	root *os.Root
}

func (tc *TestCaseRoot) PreBootstrap(_ context.Context, t *testing.T) {
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

func (tc *TestCaseRoot) Setup(_ context.Context, t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	tc.root = root
	t.Cleanup(func() { _ = root.Close() })

	mux := http.NewServeMux()
	ln := net.FreeListener(t)
	tc.Server = &http.Server{
		Addr:    ln.Addr().String(),
		Handler: mux,
	}

	mux.HandleFunc("/", tc.handleRoot)

	go func() { assert.ErrorIs(t, tc.Server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseRoot) Run(_ context.Context, t *testing.T) {
	tc.T = t
	resp, err := http.Get(fmt.Sprintf("http://%s/?path=/etc/passwd", tc.Server.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func (*TestCaseRoot) ExpectedTraces() trace.Traces {
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

func (*TestCaseRoot) ExpectedSpanCount() int {
	return 2
}

func (tc *TestCaseRoot) handleRoot(w http.ResponseWriter, r *http.Request) {
	// Without the check, the root rejects the absolute path with an error that
	// is not a security error.
	fp, err := tc.root.Open("/etc/passwd")

	assert.ErrorIs(tc.T, err, &events.BlockingSecurityEvent{})
	if events.IsSecurityError(err) {
		span, _ := tracer.SpanFromContext(r.Context())
		span.SetTag("is.security.error", true)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer fp.Close()

	w.WriteHeader(http.StatusOK)
}
