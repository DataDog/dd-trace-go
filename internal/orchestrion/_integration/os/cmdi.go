// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package os

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
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

type TestCaseCmdi struct {
	*http.Server
	*testing.T
}

func (tc *TestCaseCmdi) Setup(_ context.Context, t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("appsec does not support Windows")
	}
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}

	t.Setenv("DD_APPSEC_RULES", "../testdata/rasp-only-rules.json")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APPSEC_RASP_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1h")
	mux := http.NewServeMux()
	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: mux,
	}

	mux.HandleFunc("/", tc.handleRoot)

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseCmdi) Run(_ context.Context, t *testing.T) {
	tc.T = t
	resp, err := http.Get(fmt.Sprintf("http://%s/?command=/usr/bin/touch%%20/tmp/passwd", tc.Server.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func (*TestCaseCmdi) ExpectedTraces() trace.Traces {
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

func (tc *TestCaseCmdi) handleRoot(w http.ResponseWriter, r *http.Request) {
	command := r.URL.Query().Get("command")
	err := (&exec.Cmd{Path: command, Args: []string{command}}).Run()

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

	w.WriteHeader(http.StatusOK)
}
