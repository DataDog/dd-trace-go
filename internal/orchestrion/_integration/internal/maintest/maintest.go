// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package maintest builds a package main of this module, runs it, and collects
// the spans it reports. Rules that only apply to package main take effect
// there, and go test never builds one.
//
// Each otelc build here recompiles most of the dependency closure, which takes
// minutes in CI. Only otelc-autostart uses it, for the rule with target: main.
// Check a rule that applies to every package with a harness test case instead.
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
	"sync"
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

// URL is the agent's address, for DD_TRACE_AGENT_URL.
func (a *Agent) URL() string {
	return a.url
}

// Spans returns a copy of every span the agent collected so far, in random
// order. It is safe to call while the program under test is running.
func (a *Agent) Spans() []*agenttest.Span {
	return a.Agent.(agenttest.SpanLister).Spans()
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

// Build builds pkg, a path relative to the module root such as
// "./otelc-autostart", with otelc when withOtelc is true and with go otherwise,
// and returns the binary. An otelc build calls RequireOtelc first.
func Build(t *testing.T, pkg string, withOtelc bool) string {
	t.Helper()
	if withOtelc {
		RequireOtelc(t)
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

// BuildWithOrchestrion builds pkg, a path relative to the module root, with
// orchestrion and returns the binary. The integrations woven in are the ones
// this module's orchestrion.tool.go enables. buildArgs, such as -tags, are
// passed to go build.
func BuildWithOrchestrion(t *testing.T, pkg string, buildArgs ...string) string {
	t.Helper()
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)
	moduleDir := filepath.Dir(strings.TrimSpace(string(gomod)))

	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// CI installs an orchestrion binary on PATH, built with -cover when it
	// collects coverage. go run would build a separate, uninstrumented copy.
	var build *exec.Cmd
	args := append(append([]string{"go", "build", "-o", bin}, buildArgs...), pkg)
	if path, lookErr := exec.LookPath("orchestrion"); lookErr == nil {
		build = exec.Command(path, args...)
	} else {
		build = exec.Command("go", append([]string{"run", "github.com/DataDog/orchestrion"}, args...)...)
	}
	build.Dir = moduleDir
	build.Env = append(os.Environ(), "GOWORK=off")
	t.Log("building (slow with orchestrion: it recompiles the dependency closure)")
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "build failed:\n%s", out)
	return bin
}

// ExecUntil runs bin with env added to the environment until done reports
// true for its output so far, or the program exits, whichever comes first, and
// returns its stdout and stderr. done is checked every 50ms. exited reports
// whether the program exited on its own; otherwise it was killed. It is for
// programs that normally never exit, such as an AWS Lambda function. The test
// fails if neither happens within timeout.
func ExecUntil(t *testing.T, bin string, env []string, done func(stdout, stderr []byte) bool, timeout time.Duration) (stdout, stderr []byte, exited bool) {
	t.Helper()
	var outBuf, errBuf syncBuffer
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	require.NoError(t, cmd.Start())

	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()

	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(timeout)
	for {
		select {
		case <-waited:
			return outBuf.Bytes(), errBuf.Bytes(), true
		case <-tick.C:
			if done(outBuf.Bytes(), errBuf.Bytes()) {
				_ = cmd.Process.Kill()
				<-waited
				return outBuf.Bytes(), errBuf.Bytes(), false
			}
		case <-deadline:
			_ = cmd.Process.Kill()
			<-waited
			t.Fatalf("the app was not stopped within %s:\nstdout:\n%s\nstderr:\n%s", timeout, outBuf.Bytes(), errBuf.Bytes())
		}
	}
}

// syncBuffer is a bytes.Buffer that a running command can write to while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// Bytes returns a copy of what has been written so far.
func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
