// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
)

const victimImportPath = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/crashtracker/victim"

func TestCrashtrackerMainInjection(t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("requires an orchestrion-built test binary; run via orchestrion go test")
	}

	moduleRoot := integrationModuleRoot(t)
	tmp := t.TempDir()
	injectedBinary := filepath.Join(tmp, "victim-orchestrion")
	plainBinary := filepath.Join(tmp, "victim-plain")
	if runtime.GOOS == "windows" {
		// go build -o <path> never appends .exe on its own, even on Windows
		// (golang/go#59790, closed as intentional: an explicit -o name is
		// treated as final). exec.Command on an absolute path without a
		// recognised extension resolves it via PATHEXT-style probing
		// (os/exec's lookExtensions) and stores any failure as cmd.Err,
		// silently short-circuiting Start/CombinedOutput with no process
		// ever launched -- which is exactly the empty-output, full-timeout
		// failure this test hit on Windows CI before this fix.
		injectedBinary += ".exe"
		plainBinary += ".exe"
	}

	buildVictim(t, moduleRoot, injectedBinary, true)
	buildVictim(t, moduleRoot, plainBinary, false)

	received := make(chan []byte, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCrashtrackerRequest(r) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		body := decompressGzipBody(t, r)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	injectedOut := runVictim(t, injectedBinary, srv.URL)
	select {
	case body := <-received:
		assertInjectedCrashReport(t, body)
	case <-time.After(15 * time.Second):
		// Include the victim's own output: this timeout has been observed on
		// Windows CI with no other diagnostic (the plain-panic path through
		// crashtracker.Start() called directly, not via injection, is
		// otherwise known to work there), and discarding stdout/stderr left
		// no way to tell whether the injected Start() call ran at all, ran
		// and failed to spawn the monitor, or spawned a monitor that failed
		// to upload.
		t.Fatalf("timed out waiting for crash report from orchestrion-built victim\nvictim output:\n%s", injectedOut)
	}

	// Consume a possible second report from the orchestrion-built victim. The
	// buffer above is sized 2 so a second report (e.g. a retried upload)
	// cannot block the handler, but that same slack would let it survive into
	// the plain-victim check below and fail it with the wrong cause: "posted
	// a report without injection" when the report actually leaked from the
	// injected run above.
	select {
	case <-received:
	default:
	}

	runVictim(t, plainBinary, srv.URL)
	select {
	case body := <-received:
		t.Fatalf("plain victim posted crash report without orchestrion injection: %s", body)
	case <-time.After(3 * time.Second):
		// The plain victim is built with GOFLAGS="" specifically so it carries
		// no orchestrion instrumentation at all (buildVictim), so it has no
		// code path that could ever produce a report no matter how long this
		// waits -- unlike the positive assertion's 15s, this window isn't
		// racing a slow-but-real upload, just bounding the test's own runtime.
	}
}

// integrationModuleRoot returns the directory containing this package's
// go.mod. runtime.Caller pins this to this file's own known location, so it
// stays correct if the package ever moves a level deeper or shallower, or if
// a runner starts the test binary from a different working directory.
func integrationModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine module root: runtime.Caller failed")
	}
	// This file lives in internal/orchestrion/_integration/crashtracker, so
	// the module root with go.mod is two directory levels up.
	return filepath.Dir(filepath.Dir(file))
}

// buildVictim builds the victim binary at output, instrumented via
// orchestrionCommand when instrumented is true and via a plain, explicitly
// uninstrumented `go build` otherwise. The two modes share their timeout and
// error-reporting shape so a fix to one (e.g. the deadline) cannot miss the
// other.
func buildVictim(t *testing.T, moduleRoot, output string, instrumented bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	if instrumented {
		cmd = orchestrionCommand(ctx, "go", "build", "-o", output, victimImportPath)
	} else {
		cmd = exec.CommandContext(ctx, "go", "build", "-o", output, victimImportPath)
		// This build must never be orchestrion-instrumented -- that is the
		// whole point of the negative assertion in TestCrashtrackerMainInjection.
		// DRIVER and TOOLEXEC CI modes activate orchestrion via an explicit
		// wrapper command or -toolexec flag that this separate `go build`
		// invocation never sees, but GOFLAGS mode (.github/workflows/orchestrion.yml)
		// activates it by exporting GOFLAGS="... -toolexec=orchestrion toolexec"
		// into the whole shell session, which this process inherits via
		// os.Environ() like any other GOFLAGS content. Overriding it to empty
		// here, rather than only for that one mode, keeps this build's "plain"
		// guarantee unconditional.
		cmd.Env = append(os.Environ(), "GOFLAGS=")
	}
	cmd.Dir = moduleRoot

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("build victim (instrumented=%v): timeout", instrumented)
	}
	if err != nil {
		t.Fatalf("build victim (instrumented=%v): %v\n%s", instrumented, err, out)
	}
}

// orchestrionCommand invokes the orchestrion CLI, preferring a PATH-resolved
// binary over `go run`. CI installs an orchestrion binary to GOBIN
// specifically so steps like this one can use it directly, and for
// coverage-collection runs that binary is built with -cover instead: a
// nested `go run github.com/DataDog/orchestrion` here would compile its own
// uninstrumented copy, silently dropping this build out of the coverage
// report. Falls back to `go run` when no such binary is on PATH, matching
// the plain command this package's README documents for local development.
func orchestrionCommand(ctx context.Context, args ...string) *exec.Cmd {
	if path, err := exec.LookPath("orchestrion"); err == nil {
		return exec.CommandContext(ctx, path, args...)
	}
	return exec.CommandContext(ctx, "go", append([]string{"run", "github.com/DataDog/orchestrion"}, args...)...)
}

// runVictim runs binary and returns its combined stdout+stderr. The caller
// decides whether to surface it: a panicking victim's own crash dump
// (written to stderr, independent of and before any crashtracker upload) is
// the most direct evidence available when a report doesn't arrive as
// expected.
func runVictim(t *testing.T, binary, agentURL string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary) //nolint:gosec
	cmd.Env = append(filterInjectionEnv(os.Environ()),
		"DD_TRACE_AGENT_URL="+agentURL,
		"DD_CRASHTRACKING_ENABLED=true",
		"DD_TRACE_ENABLED=false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=false",
		"DD_REMOTE_CONFIGURATION_ENABLED=false",
		// Lets the profiler's injected func init() actually call Start, so the
		// victim's own ordering check (globalconfig/traceprof, victim/main.go)
		// has something real to observe instead of trivially seeing "not
		// started" for a profiler that was never going to start either way.
		"DD_PROFILING_ENABLED=true",
		// Without an explicit service, tracer.Start() (ddtrace/tracer/option.go)
		// deliberately skips globalconfig.SetServiceName so contribs keep
		// computing their own default -- meaning globalconfig.ServiceName()
		// would read empty even on a fully successful, correctly-ordered
		// Start(), and the victim's ordering check would misread that as
		// "tracer hasn't run yet". An explicit service routes through the
		// branch that does set it.
		"DD_SERVICE=crashtracker-ordering-proof",
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("run victim %q: timeout", binary)
	}
	// A non-zero exit from the victim's own panic is expected; a *exec.Error
	// (failed to even start, e.g. cmd.Err from a failed Windows extension
	// lookup) is not, and CombinedOutput would otherwise return it silently
	// alongside empty output.
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		t.Fatalf("run victim %q: %v", binary, execErr)
	}
	return out
}

// filterInjectionEnv strips variables that must not pollute the victim's
// environment. DD_API_KEY/DD-API-KEY (the hyphenated alias internal/env also
// resolves DD_API_KEY from) and DD_SITE are stripped because crashtracker
// prefers the agentless path whenever an API key is set, ahead of
// DD_TRACE_AGENT_URL: an ambient key from a developer's shell or CI
// environment would send the victim's report to the real intake instead of
// srv, and the caller would wait out its full 15s timeout for a report that
// never arrives.
func filterInjectionEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "DD_TRACE_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_ENABLED=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_IS_MONITOR_PROCESS=") ||
			strings.HasPrefix(kv, "DD_TRACE_ENABLED=") ||
			strings.HasPrefix(kv, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=") ||
			strings.HasPrefix(kv, "DD_REMOTE_CONFIGURATION_ENABLED=") ||
			strings.HasPrefix(kv, "DD_API_KEY=") ||
			strings.HasPrefix(kv, "DD-API-KEY=") ||
			strings.HasPrefix(kv, "DD_SITE=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

func assertInjectedCrashReport(t *testing.T, body []byte) {
	t.Helper()
	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("unmarshal crash report: %v\n%s", err, body)
	}
	if report["ddsource"] != "crashtracker" {
		t.Errorf("ddsource = %q, want crashtracker", report["ddsource"])
	}
	errObj, ok := report["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or not an object: %v", report["error"])
	}
	if errObj["is_crash"] != true {
		t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
	}
	if got, _ := errObj["type"].(string); got != "panic" {
		t.Errorf("error.type = %q, want panic", got)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "orchestrion injection victim crash") {
		t.Errorf("error.message = %q, want injection victim crash", msg)
	}
	// The tracer and profiler aspects inject func init() calls, which the Go
	// runtime guarantees complete before main() runs -- these confirm that
	// guarantee actually held for this build, rather than assuming it (see
	// victim/main.go for how the victim itself observes this).
	if !strings.Contains(msg, "tracer_started=true") {
		t.Errorf("error.message = %q, want tracer_started=true: the injected tracer.Start() should have run before crashtracker.Start()", msg)
	}
	if !strings.Contains(msg, "profiler_started=true") {
		t.Errorf("error.message = %q, want profiler_started=true: the injected profiler.Start() should have run before crashtracker.Start()", msg)
	}
}
