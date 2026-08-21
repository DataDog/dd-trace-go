// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAgent is a real HTTP trace intake. The in-process agenttest.Agent does not
// work here: it delivers requests through an in-process RoundTripper and never
// opens a socket that a child process could reach.
//
// Payloads stay raw. An operation name appears verbatim in the msgpack body, so
// matching on bytes avoids depending on the wire format.
type stubAgent struct {
	server *httptest.Server

	mu       sync.Mutex
	payloads [][]byte
}

func newStubAgent(t *testing.T) *stubAgent {
	t.Helper()

	a := &stubAgent{}
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"endpoints":["/v0.4/traces"]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if body, err := io.ReadAll(r.Body); err == nil && len(body) > 0 {
			a.mu.Lock()
			a.payloads = append(a.payloads, body)
			a.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"rate_by_service":{}}`)
	})

	a.server = httptest.NewServer(mux)
	t.Cleanup(a.server.Close)
	return a
}

func (a *stubAgent) reported(operation string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.payloads {
		if bytes.Contains(p, []byte(operation)) {
			return true
		}
	}
	return false
}

func (a *stubAgent) requestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.payloads)
}

// TestOtelcStartsTracer is the only check that the tracer lifecycle rule in
// ddtrace/tracer/otelc.yaml works. Every other suite runs under the integration
// harness, which calls tracertest.Bootstrap itself and so has a running tracer
// either way.
//
// The plain build is the control: without it, a passing otelc case would only
// show that a span reached an agent, not that otelc started the tracer.
func TestOtelcStartsTracer(t *testing.T) {
	if _, err := exec.LookPath("otelc"); err != nil {
		t.Skip("otelc is not on PATH; install it from " +
			"github.com/open-telemetry/opentelemetry-go-compile-instrumentation")
	}

	// Built from the module directory so otelc finds ../otel.instrumentation.go,
	// which is what pulls in the foundation rules.
	moduleDir, err := filepath.Abs("..")
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		instrument bool
		wantSpan   bool
	}{
		{name: "otelc build starts the tracer", instrument: true, wantSpan: true},
		{name: "plain build starts nothing", instrument: false, wantSpan: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// `go build -o` appends .exe on Windows, so the path handed to
			// exec.Command has to carry it or the run fails with
			// "executable file not found in %PATH%".
			bin := filepath.Join(t.TempDir(), "otelc-autostart")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}

			var build *exec.Cmd
			if tc.instrument {
				// A private work dir keeps this build from fighting over
				// .otelc-build with whatever invoked the test.
				build = exec.Command("otelc", "--work-dir", t.TempDir(),
					"go", "build", "-o", bin, "./otelc-autostart")
			} else {
				build = exec.Command("go", "build", "-o", bin, "./otelc-autostart")
			}
			build.Dir = moduleDir
			// Workspace mode resolves modules differently from the module graph
			// otelc analyzes, so both builds are pinned to module mode.
			build.Env = append(os.Environ(), "GOWORK=off")

			t.Log("building (slow for the otelc case: it recompiles the dependency closure)")
			out, err := build.CombinedOutput()
			require.NoErrorf(t, err, "build failed:\n%s", out)

			agent := newStubAgent(t)

			run := exec.Command(bin)
			run.Env = append(os.Environ(),
				"DD_TRACE_AGENT_URL="+agent.server.URL,
				"DD_TRACE_STARTUP_LOGS=false",
			)
			runOut, err := run.CombinedOutput()
			require.NoErrorf(t, err, "running the app failed:\n%s", runOut)
			t.Logf("app output:\n%s", runOut)

			// No polling either way: the app's own `defer tracer.Stop()` flushes
			// synchronously, and the child process has already exited.
			if tc.wantSpan {
				assert.Truef(t, agent.reported(spanName),
					"no %q span reached the agent across %d payload(s): otelc did not start the tracer",
					spanName, agent.requestCount())
				return
			}
			assert.Falsef(t, agent.reported(spanName),
				"a plain build reported %q, so a passing otelc case could not be attributed to otelc",
				spanName)
			assert.Zerof(t, agent.requestCount(),
				"a plain build talked to the agent %d time(s); no tracer should have started",
				agent.requestCount())
		})
	}
}
