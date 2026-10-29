// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package maintest builds a package main of this module, runs it, and collects
// the spans it reports. Rules that only apply to package main take effect
// there, and go test never builds one.
package maintest

import (
	"bytes"
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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
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

// RequireOtelc skips the test when otelc is not on PATH, or fails it when the
// test binary was itself built with otelc, where a missing binary means a broken
// install rather than an optional tool.
func RequireOtelc(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("otelc"); err == nil {
		return
	}
	const msg = "otelc is not on PATH; install it from " +
		"github.com/open-telemetry/opentelemetry-go-compile-instrumentation"
	if otelc.Enabled() {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// Build builds pkg, a path relative to the module root such as "./chi/mainapp",
// with otelc when withOtelc is true and with go otherwise, and returns the
// binary. An otelc build calls RequireOtelc first.
func Build(t *testing.T, pkg string, withOtelc bool) string {
	t.Helper()
	if withOtelc {
		RequireOtelc(t)
	}
	bin := binPath(t)
	if withOtelc {
		// A private work dir keeps this build from fighting over .otelc-build
		// with whatever invoked the test.
		build(t, exec.Command("otelc", "--work-dir", t.TempDir(), "go", "build", "-o", bin, pkg))
	} else {
		build(t, exec.Command("go", "build", "-o", bin, pkg))
	}
	return bin
}

// BuildWithOrchestrion builds pkg, a path relative to the module root, with
// orchestrion and returns the binary. The integrations woven in are the ones
// this module's orchestrion.tool.go enables.
func BuildWithOrchestrion(t *testing.T, pkg string) string {
	t.Helper()
	bin := binPath(t)
	// CI installs an orchestrion binary on PATH, built with -cover when it
	// collects coverage. go run would build a separate, uninstrumented copy.
	var cmd *exec.Cmd
	if path, err := exec.LookPath("orchestrion"); err == nil {
		cmd = exec.Command(path, "go", "build", "-o", bin, pkg)
	} else {
		cmd = exec.Command("go", "run", "github.com/DataDog/orchestrion", "go", "build", "-o", bin, pkg)
	}
	build(t, cmd)
	return bin
}

func binPath(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	return bin
}

func build(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	// The test runs in its package directory, inside this module.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)
	cmd.Dir = filepath.Dir(strings.TrimSpace(string(gomod)))
	// Workspace mode resolves modules differently from the module graph otelc
	// analyzes, so all builds are pinned to module mode.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	t.Log("building (slow with otelc or orchestrion: they recompile the dependency closure)")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "build failed:\n%s", out)
}

// ExecUntil runs bin with env added to the environment until stop is closed,
// then kills it and returns its stdout and stderr. It is for programs that
// never exit on their own, such as an AWS Lambda function. The test fails if
// the program exits first, or if stop is not closed within timeout.
func ExecUntil(t *testing.T, bin string, env []string, stop <-chan struct{}, timeout time.Duration) (stdout, stderr []byte) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	require.NoError(t, cmd.Start())

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case <-stop:
		_ = cmd.Process.Kill()
		<-exited
	case err := <-exited:
		t.Fatalf("the app exited before it was stopped (%v):\nstdout:\n%s\nstderr:\n%s", err, &outBuf, &errBuf)
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("the app was not stopped within %s:\nstdout:\n%s\nstderr:\n%s", timeout, &outBuf, &errBuf)
	}
	return outBuf.Bytes(), errBuf.Bytes()
}

// Run builds pkg with otelc, runs it, and returns the agent it reported to.
func Run(t *testing.T, pkg string) *Agent {
	t.Helper()
	bin := Build(t, pkg, true)
	agent := NewAgent(t)
	agent.Exec(t, bin)
	return agent
}
