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
	"path/filepath"
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

	buildInjectedVictim(t, moduleRoot, injectedBinary)
	buildPlainVictim(t, moduleRoot, plainBinary)

	received := make(chan []byte, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCrashtrackerRequest(r) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	runVictim(t, injectedBinary, srv.URL)
	select {
	case body := <-received:
		assertInjectedCrashReport(t, body)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for crash report from orchestrion-built victim")
	}

	runVictim(t, plainBinary, srv.URL)
	select {
	case body := <-received:
		t.Fatalf("plain victim posted crash report without orchestrion injection: %s", body)
	case <-time.After(3 * time.Second):
	}
}

func integrationModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Dir(wd)
}

func buildInjectedVictim(t *testing.T, moduleRoot, output string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "github.com/DataDog/orchestrion", "go", "build", "-o", output, victimImportPath)
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("build orchestrion victim: timeout")
	}
	if err != nil {
		t.Fatalf("build orchestrion victim: %v\n%s", err, out)
	}
}

func buildPlainVictim(t *testing.T, moduleRoot, output string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", output, victimImportPath)
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("build plain victim: timeout")
	}
	if err != nil {
		t.Fatalf("build plain victim: %v\n%s", err, out)
	}
}

func runVictim(t *testing.T, binary, agentURL string) {
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
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run() // Non-zero panic exit is expected for both victims.
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("run victim %q: timeout", binary)
	}
}

func filterInjectionEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "DD_TRACE_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_ENABLED=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_IS_MONITOR_PROCESS=") ||
			strings.HasPrefix(kv, "DD_TRACE_ENABLED=") ||
			strings.HasPrefix(kv, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=") ||
			strings.HasPrefix(kv, "DD_REMOTE_CONFIGURATION_ENABLED=") {
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
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "orchestrion injection victim crash") {
		t.Errorf("error.message = %q, want injection victim crash", msg)
	}
}
