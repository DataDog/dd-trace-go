// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtracker_test contains end-to-end tests for the crashtracker
// package. The tests exercise the full chain: crash victim process →
// monitor child → mock Error Tracking intake.
//
// Two subprocess roles are driven by the _CRASHTRACKER_E2E env var:
//
//   - "panic": calls Start(), then panics — the monitor uploads the report.
//   - "clean": calls Start() + Stop() and exits cleanly — no report expected.
//
// TestMain intercepts these roles before any test function runs.
package crashtracker_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const e2eRoleEnv = "_CRASHTRACKER_E2E"

// TestMain intercepts re-executions of the test binary that serve as crash
// victim subprocesses. The crashtracker package's own init() already handles
// the monitor-grandchild role (DD_CRASHTRACKING_IS_MONITOR_PROCESS=1), so
// TestMain only needs to handle the crash-victim roles.
func TestMain(m *testing.M) {
	switch os.Getenv(e2eRoleEnv) {
	case "panic":
		// Crash victim: start the crashtracker (spawns monitor grandchild) then panic.
		// DD_TRACE_AGENT_URL in the env tells both spawnMonitor and the monitor
		// grandchild where to send the report.
		if err := crashtracker.Start(); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic("e2e test crash")

	case "clean":
		// Clean exit: start and immediately stop. The monitor should see EOF on
		// the pipe with no data and exit without uploading.
		if err := crashtracker.Start(); err != nil {
			os.Exit(1)
		}
		crashtracker.Stop()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestE2ECrashReport_Panic verifies the full crash → monitor → intake chain.
// It spawns a crash-victim subprocess that panics, then waits for the monitor
// grandchild to POST a valid errorsintake report to the mock server.
func TestE2ECrashReport_Panic(t *testing.T) {
	t.Parallel()

	// Mock Error Tracking intake: capture the first POST body.
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCanonicalAgentRequest(t, r)
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// Spawn the crash-victim subprocess. It inherits DD_TRACE_AGENT_URL so
	// both its own buildRequestAndClient and the monitor grandchild's
	// AgentURLFromEnv() resolve to the mock server.
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"=panic",
		"DD_TRACE_AGENT_URL="+srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn crash victim: %v", err)
	}
	// The victim panics, so non-zero exit is expected.
	_ = cmd.Wait()

	// Wait for the monitor grandchild to deliver the crash report.
	select {
	case body := <-received:
		assertCrashReport(t, body, "panic", "e2e test crash")

	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for crash report from monitor")
	}
}

// TestE2ECrashReport_CleanExit verifies that a clean process exit does NOT
// produce a crash report: the monitor sees EOF with no data and exits quietly.
func TestE2ECrashReport_CleanExit(t *testing.T) {
	t.Parallel()

	posted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case posted <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"=clean",
		"DD_TRACE_AGENT_URL="+srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		// Clean exit should succeed.
		t.Fatalf("crash victim exited non-zero: %v", err)
	}

	// Give the monitor a short window to (incorrectly) post a report.
	select {
	case <-posted:
		t.Error("crash report was posted on clean exit; want none")
	case <-time.After(500 * time.Millisecond):
	}
}

func assertCanonicalAgentRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Path != "/evp_proxy/v4/api/v2/errorsintake" {
		t.Errorf("path = %q, want /evp_proxy/v4/api/v2/errorsintake", r.URL.Path)
	}
	if got := r.Header.Get("X-Datadog-EVP-Subdomain"); got != "error-tracking-intake" {
		t.Errorf("EVP subdomain = %q, want error-tracking-intake", got)
	}
}

// assertCrashReport validates the structure and key fields of an errorsintake
// crash report payload.
func assertCrashReport(t *testing.T, body []byte, wantType, wantMsgSubstr string) {
	t.Helper()

	if len(body) == 0 {
		t.Fatal("received empty report body")
	}

	report := assertRFC0013Body(t, body)
	errObj := report["error"].(map[string]any)

	if got, _ := errObj["type"].(string); got != wantType {
		t.Errorf("error.type = %q, want %q", got, wantType)
	}

	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, wantMsgSubstr) {
		t.Errorf("error.message = %q, want it to contain %q", msg, wantMsgSubstr)
	}

	ddtags, _ := report["ddtags"].(string)
	if !strings.Contains(ddtags, "language:go") {
		t.Errorf("ddtags = %q, want it to contain \"language:go\"", ddtags)
	}
}

func assertRFC0013Body(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("unmarshal report: %v\nbody: %s", err, body)
	}
	if got := report["ddsource"]; got != "crashtracker" {
		t.Errorf("ddsource = %q, want \"crashtracker\"", got)
	}
	if _, ok := report["timestamp"].(float64); !ok {
		t.Errorf("timestamp type = %T, want number", report["timestamp"])
	}
	if ddtags, _ := report["ddtags"].(string); ddtags == "" {
		t.Error("ddtags is empty")
	}

	errObj, ok := report["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or not an object; report: %v", report)
	}
	if errObj["is_crash"] != true {
		t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
	}
	if errObj["source_type"] != "Crashtracking" {
		t.Errorf("error.source_type = %q, want \"Crashtracking\"", errObj["source_type"])
	}
	if _, ok := errObj["type"]; !ok {
		t.Error("error.type missing")
	}

	stack, ok := errObj["stack"].(map[string]any)
	if !ok {
		t.Error("error.stack missing or not an object")
	} else {
		if stack["format"] != "Datadog Crashtracker 1.0" {
			t.Errorf("error.stack.format = %q, want Datadog Crashtracker 1.0", stack["format"])
		}
		frames, _ := stack["frames"].([]any)
		if len(frames) == 0 {
			t.Error("error.stack.frames is empty; want at least one frame")
		}
	}

	threads, _ := errObj["threads"].([]any)
	if len(threads) == 0 {
		t.Error("error.threads is empty; want at least one goroutine")
	}
	crashedCount := 0
	for _, th := range threads {
		thMap, _ := th.(map[string]any)
		if thMap["crashed"] == true {
			crashedCount++
		}
	}
	if crashedCount != 1 {
		t.Errorf("crashed goroutine count = %d, want 1", crashedCount)
	}

	osInfo, ok := report["os_info"].(map[string]any)
	if !ok {
		t.Error("os_info missing")
	} else if architecture, _ := osInfo["architecture"].(string); architecture == "" {
		t.Error("os_info.architecture is empty")
	}
	return report
}

// filterE2EEnv strips variables that must not pollute the subprocess environment.
func filterE2EEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, e2eRoleEnv+"=") ||
			strings.HasPrefix(kv, "DD_TRACE_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_ENABLED=") ||
			strings.HasPrefix(kv, "DD_TRACE_ENABLED=") ||
			strings.HasPrefix(kv, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=") ||
			strings.HasPrefix(kv, "DD_REMOTE_CONFIGURATION_ENABLED=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}
