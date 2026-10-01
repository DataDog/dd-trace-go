// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package maintest builds a package main of this module, runs it, and collects
// the spans it reports. Rules that only apply to package main take effect
// there, and go test never builds one.
package maintest

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
)

// Agent is the test agent the harness suites use, served on a local port so
// that a separate process can report to it.
type Agent struct {
	agenttest.Agent

	url      string
	requests atomic.Int32
}

// NewAgent starts a test agent on a local port.
func NewAgent(t *testing.T) *Agent {
	t.Helper()
	agent, err := tracertest.StartAgent(t)
	require.NoError(t, err)

	a := &Agent{Agent: agent}
	transport := agent.Transport()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		resp, err := transport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	a.url = srv.URL
	return a
}

// Requests reports how many requests the agent received.
func (a *Agent) Requests() int {
	return int(a.requests.Load())
}

// Exec runs bin to completion, reporting to a, and returns its output.
func (a *Agent) Exec(t *testing.T, bin string) []byte {
	t.Helper()
	run := exec.Command(bin)
	run.Env = append(os.Environ(),
		"DD_TRACE_AGENT_URL="+a.url,
		"DD_TRACE_STARTUP_LOGS=false",
	)
	out, err := run.CombinedOutput()
	require.NoErrorf(t, err, "running the app failed:\n%s", out)
	return out
}

// Build builds pkg, a path relative to the module root such as "./chi/mainapp",
// with otelc when withOtelc is true and with go otherwise, and returns the
// binary. An otelc build skips the test when otelc is not on PATH.
func Build(t *testing.T, pkg string, withOtelc bool) string {
	t.Helper()
	if _, err := exec.LookPath("otelc"); withOtelc && err != nil {
		t.Skip("otelc is not on PATH; install it from " +
			"github.com/open-telemetry/opentelemetry-go-compile-instrumentation")
	}

	// The test runs in its package directory, inside this module.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)
	moduleDir := filepath.Dir(strings.TrimSpace(string(gomod)))

	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	var build *exec.Cmd
	if withOtelc {
		// A private work dir keeps this build from fighting over .otelc-build
		// with whatever invoked the test.
		build = exec.Command("otelc", "--work-dir", t.TempDir(), "go", "build", "-o", bin, pkg)
	} else {
		build = exec.Command("go", "build", "-o", bin, pkg)
	}
	build.Dir = moduleDir
	// Workspace mode resolves modules differently from the module graph otelc
	// analyzes, so both builds are pinned to module mode.
	build.Env = append(os.Environ(), "GOWORK=off")
	t.Log("building (slow with otelc: it recompiles the dependency closure)")
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "build failed:\n%s", out)
	return bin
}

// Run builds pkg with otelc, runs it, and returns the agent it reported to.
func Run(t *testing.T, pkg string) *Agent {
	t.Helper()
	bin := Build(t, pkg, true)
	agent := NewAgent(t)
	agent.Exec(t, bin)
	return agent
}
