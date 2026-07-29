// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtracker_test contains end-to-end tests for the crashtracker
// package. The tests exercise the full chain: crash victim process →
// monitor child → mock Error Tracking intake.
//
// Subprocess roles are driven by the _CRASHTRACKER_E2E env var:
//
//   - "panic": calls Start() via DD_* env config, then panics — the monitor
//     uploads the report using the env-resolved config.
//   - "panic-with-options": calls Start(opts...) with only programmatic
//     options (no DD_TRACE_AGENT_URL etc. in the environment), then panics —
//     proves options cross the process boundary to the monitor.
//   - "clean": calls Start() and exits cleanly — no report expected. There is
//     no Stop to call: process exit alone closes the crash pipe.
//
// TestMain intercepts these roles before any test function runs.
package crashtracker_test

import (
	"compress/gzip"
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

	case "panic-with-options":
		// Crash victim: start with only programmatic options, no DD_TRACE_AGENT_URL
		// in the environment. If options are not forwarded to the monitor, the
		// monitor falls back to the default agent URL and the mock server never
		// receives a report, timing out the test.
		if err := crashtracker.Start(
			crashtracker.WithService("e2e-options-svc"),
			crashtracker.WithEnv("e2e-options-env"),
			crashtracker.WithVersion("9.9.9"),
			crashtracker.WithAgentURL(os.Getenv("_CRASHTRACKER_E2E_AGENT_URL")),
		); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic("e2e options crash")

	case "clean":
		// Clean exit: start, then exit without a crash. The monitor should see
		// EOF on the pipe with no data and exit without uploading. Process exit
		// closes the pipe; there is no Stop to call.
		if err := crashtracker.Start(); err != nil {
			os.Exit(1)
		}
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
		body := decompressGzipBody(t, r)
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

// TestE2ECrashReport_PanicWithOptions verifies that options passed to Start
// (not DD_* env vars) reach the monitor process and control its upload
// destination and tags. This is the end-to-end proof that config forwarding
// across the process boundary works.
func TestE2ECrashReport_PanicWithOptions(t *testing.T) {
	t.Parallel()

	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCanonicalAgentRequest(t, r)
		body := decompressGzipBody(t, r)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// Deliberately do NOT set DD_TRACE_AGENT_URL: the subprocess role passes
	// the mock server URL via WithAgentURL instead, through a side-channel env
	// var the test controls (not a crashtracker-recognised variable).
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"=panic-with-options",
		"_CRASHTRACKER_E2E_AGENT_URL="+srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn crash victim: %v", err)
	}
	_ = cmd.Wait()

	select {
	case body := <-received:
		report := assertRFC0013Body(t, body)
		errObj := report["error"].(map[string]any)
		if got, _ := errObj["message"].(string); !strings.Contains(got, "e2e options crash") {
			t.Errorf("error.message = %q, want it to contain %q", got, "e2e options crash")
		}
		ddtags, _ := report["ddtags"].(string)
		for _, want := range []string{"service:e2e-options-svc", "env:e2e-options-env", "version:9.9.9"} {
			if !strings.Contains(ddtags, want) {
				t.Errorf("ddtags = %q, want it to contain %q", ddtags, want)
			}
		}

	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for crash report; options may not be forwarded to the monitor")
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
	if got := r.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
}

// decompressGzipBody reads and gunzips the request body.
func decompressGzipBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	gz, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatalf("create gzip reader: %v", err)
	}
	defer gz.Close()
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip stream: %v", err)
	}
	return body
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
	if !strings.Contains(ddtags, "language_name:go") {
		t.Errorf("ddtags = %q, want it to contain \"language_name:go\"", ddtags)
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
	ddtags, _ := report["ddtags"].(string)
	if ddtags == "" {
		t.Error("ddtags is empty")
	}
	// data_schema_version, incomplete, is_crash, and uuid travel as ddtags
	// entries, not top-level fields: libdatadog's own wire payload
	// (ErrorsIntakePayload in errors_intake.rs) has no such top-level fields
	// either, folding them into ddtags via build_crash_info_tags instead.
	// Asserted against the schema version literal directly (not the
	// package's internal constant), since this is a black-box check of the
	// wire contract, not of the implementation.
	const wantSchemaVersion = "1.8"
	if !strings.Contains(ddtags, "data_schema_version:"+wantSchemaVersion) {
		t.Errorf("ddtags = %q, want it to contain %q", ddtags, "data_schema_version:"+wantSchemaVersion)
	}
	if !strings.Contains(ddtags, "uuid:") {
		t.Errorf("ddtags = %q, want a uuid entry", ddtags)
	}
	if !strings.Contains(ddtags, "incomplete:") {
		t.Errorf("ddtags = %q, want an incomplete entry", ddtags)
	}
	if !strings.Contains(ddtags, "is_crash:true") {
		t.Errorf("ddtags = %q, want it to contain %q", ddtags, "is_crash:true")
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
	} else {
		if architecture, _ := osInfo["architecture"].(string); architecture == "" {
			t.Error("os_info.architecture is empty")
		}
		if version, _ := osInfo["version"].(string); version == "" {
			t.Error("os_info.version is empty")
		}
	}
	return report
}

// filterE2EEnv strips variables that must not pollute the subprocess environment.
func filterE2EEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, e2eRoleEnv+"=") ||
			strings.HasPrefix(kv, "_CRASHTRACKER_E2E_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_TRACE_AGENT_URL=") ||
			strings.HasPrefix(kv, "DD_CRASHTRACKING_ENABLED=") ||
			strings.HasPrefix(kv, "DD_TRACE_ENABLED=") ||
			strings.HasPrefix(kv, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=") ||
			strings.HasPrefix(kv, "DD_REMOTE_CONFIGURATION_ENABLED=") ||
			// An ambient DD_API_KEY (set in a developer's shell or a CI
			// environment that dogfoods its own tracer) would otherwise make
			// defaultConfig resolve the crash-victim subprocess into agentless
			// mode, which takes precedence over the test's WithAgentURL/
			// DD_TRACE_AGENT_URL (see buildRequestAndClient). The subprocess
			// would then try to upload its synthetic crash to the real
			// configured site instead of the test's mock server, and the test
			// would hang waiting on a report that never arrives. DD_SITE
			// shapes that same agentless target, so it is stripped alongside it.
			strings.HasPrefix(kv, "DD_API_KEY=") ||
			strings.HasPrefix(kv, "DD_SITE=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

// TestFilterE2EEnvStripsAPIKeyAndSite proves the fix for the bug where an
// ambient DD_API_KEY survived filtering and forced a crash-victim subprocess
// into agentless mode, bypassing the test's mock intake entirely.
func TestFilterE2EEnvStripsAPIKeyAndSite(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"DD_API_KEY=some-real-key",
		"DD_SITE=datadoghq.com",
		"DD_ENV=prod",
	}
	out := filterE2EEnv(in)

	for _, kv := range out {
		if strings.HasPrefix(kv, "DD_API_KEY=") || strings.HasPrefix(kv, "DD_SITE=") {
			t.Errorf("filterE2EEnv(%v) = %v, want DD_API_KEY/DD_SITE stripped", in, out)
		}
	}
	found := false
	for _, kv := range out {
		if kv == "DD_ENV=prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("filterE2EEnv(%v) = %v, want DD_ENV to survive (only DD_API_KEY/DD_SITE are stripped)", in, out)
	}
}
