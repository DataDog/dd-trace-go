// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const (
	// e2eRoleEnv drives subprocess behaviour in TestMain.
	e2eRoleEnv = "_CRASHTRACKER_E2E_ORCH"

	// crashRoleOrch is the panic-victim role. The subprocess deliberately does
	// NOT call crashtracker.Start() itself — the monitor must be spawned by the
	// orchestrion-injected Start() call that fires before TestMain runs.
	crashRoleOrch = "panic"

	// orchCrashMsg is the panic string asserted in the received crash report.
	orchCrashMsg = "orchestrion e2e crash"
)

// TestCase is the orchestrion integration test for the crashtracker aspect.
//
// It verifies the full crash-reporting chain under orchestrion instrumentation:
//  1. orchestrion injects crashtracker.Start() as the first statement of main().
//  2. A crash-victim subprocess panics WITHOUT calling Start() explicitly.
//  3. The monitor grandchild (spawned by the injected Start()) reads the crash
//     dump and uploads a structured report to the mock intake.
//
// If orchestrion fails to inject Start(), no monitor is spawned, no crash pipe
// is wired, and the test times out — proving the injection is load-bearing.
type TestCase struct {
	mockSrv  *httptest.Server
	received chan []byte
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.received = make(chan []byte, 1)
	tc.mockSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case tc.received <- body:
		default:
		}
		w.WriteHeader(202)
	}))
	t.Cleanup(tc.mockSrv.Close)
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	cmd := spawnSubprocess(t, crashRoleOrch, tc.mockSrv.URL)
	_ = cmd.Wait() // non-zero exit expected (panic)

	select {
	case body := <-tc.received:
		assertOrchCrashReport(t, body)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for crash report; " +
			"orchestrion may not have injected crashtracker.Start() into main()")
	}
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{}
}

// spawnSubprocess re-execs this binary as a crash-victim subprocess. It is
// called from TestCase.Run. Only runs when the binary was built with orchestrion.
func spawnSubprocess(t *testing.T, role, agentURL string) *exec.Cmd {
	t.Helper()
	if !built.WithOrchestrion {
		t.Skip("subprocess e2e requires orchestrion-built binary; run via orchestrion go test")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false") //nolint:gosec
	cmd.Env = append(filterOrchEnv(os.Environ()),
		e2eRoleEnv+"="+role,
		"DD_TRACE_AGENT_URL="+agentURL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn subprocess: %v", err)
	}
	return cmd
}

// filterOrchEnv strips variables that must not pollute the subprocess environment.
func filterOrchEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, e2eRoleEnv+"=") ||
			strings.HasPrefix(kv, "DD_TRACE_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_ENABLED=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

// assertOrchCrashReport validates the key fields of the received crash report.
func assertOrchCrashReport(t *testing.T, body []byte) {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("received empty crash report body")
	}
	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("unmarshal crash report: %v\nbody: %s", err, body)
	}
	if report["ddsource"] != "crashtracker" {
		t.Errorf("ddsource = %q, want \"crashtracker\"", report["ddsource"])
	}
	errObj, ok := report["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or not an object")
	}
	if errObj["is_crash"] != true {
		t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
	}
	if got, _ := errObj["type"].(string); got != "panic" {
		t.Errorf("error.type = %q, want \"panic\"", got)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, orchCrashMsg) {
		t.Errorf("error.message = %q, want it to contain %q", msg, orchCrashMsg)
	}
}
