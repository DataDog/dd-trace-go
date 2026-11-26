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
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAllowed opens a file and starts a process that the RASP rules
// allow, inside a request, and checks that the calls return their real
// results.
type TestCaseAllowed struct {
	*http.Server
	*testing.T
	path string
}

const allowedContent = "allowed"

func (tc *TestCaseAllowed) PreBootstrap(_ context.Context, t *testing.T) {
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

func (tc *TestCaseAllowed) Setup(_ context.Context, t *testing.T) {
	tc.path = filepath.Join(t.TempDir(), "allowed.txt")
	require.NoError(t, os.WriteFile(tc.path, []byte(allowedContent), 0o600))

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

func (tc *TestCaseAllowed) Run(_ context.Context, t *testing.T) {
	tc.T = t
	resp, err := http.Get(fmt.Sprintf("http://%s/?name=whatever", tc.Server.Addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (*TestCaseAllowed) ExpectedTraces() trace.Traces {
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
						"component": "net/http",
						"span.kind": "server",
					},
				},
			},
		},
	}
}

func (*TestCaseAllowed) ExpectedSpanCount() int {
	return 2
}

func (tc *TestCaseAllowed) handleRoot(w http.ResponseWriter, _ *http.Request) {
	content, err := os.ReadFile(tc.path)
	if !assert.NoError(tc.T, err) || !assert.Equal(tc.T, allowedContent, string(content)) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	cmd := exec.Command("true")
	if !assert.NoError(tc.T, cmd.Run()) || !assert.True(tc.T, cmd.ProcessState.Success()) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
