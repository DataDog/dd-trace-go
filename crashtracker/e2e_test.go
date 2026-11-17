// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtracker_test exercises supervisor, application, and intake
// processes through the public API. TestMain selects application helper roles.
package crashtracker_test

import (
	"bytes"
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

	"github.com/google/uuid"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

// ddTagValue returns the value of the first "key:value" entry in a
// comma-separated ddtags string, and whether it was present at all. Local
// counterpart of intake_assert_test.go's identically-named helper: the
// internal/external package split forces the duplication (see this file's
// package doc), same as assertCanonicalAgentRequest/assertRFC0013Body below.
func ddTagValue(ddtags, key string) (string, bool) {
	for kv := range strings.SplitSeq(ddtags, ",") {
		if k, v, ok := strings.Cut(kv, ":"); ok && k == key {
			return v, true
		}
	}
	return "", false
}

type optionsTransport struct{}

func (optionsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("X-Crashtracker-Options", "custom-client")
	return http.DefaultTransport.RoundTrip(r)
}

const e2eRoleEnv = "_CRASHTRACKER_E2E"

// TestMain selects the same application role in both re-executed processes.
func TestMain(m *testing.M) {
	switch os.Getenv(e2eRoleEnv) {
	case "panic":
		if err := crashtracker.Start(); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic("e2e test crash")

	case "panic-with-options":
		if err := crashtracker.Start(
			crashtracker.WithService("e2e-options-svc"),
			crashtracker.WithEnv("e2e-options-env"),
			crashtracker.WithVersion("9.9.9"),
			crashtracker.WithAgentURL(os.Getenv("_CRASHTRACKER_E2E_AGENT_URL")),
			crashtracker.WithHTTPClient(&http.Client{Transport: optionsTransport{}}),
		); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic("e2e options crash")

	case "clean":
		if err := crashtracker.Start(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)

	case "panic-many-goroutines", "panic-large-dump":
		// GOTRACEBACK=all includes enough goroutines to exercise truncation.
		if err := crashtracker.Start(); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		manyGoroutines := 150
		if os.Getenv(e2eRoleEnv) == "panic-large-dump" {
			manyGoroutines = 2000
		}
		block := make(chan struct{})
		for range manyGoroutines {
			go func() { <-block }()
		}
		time.Sleep(200 * time.Millisecond) // let the goroutines actually park before crashing
		panic("e2e many-goroutines crash")
	}
	os.Exit(m.Run())
}

// TestE2ECrashReport_Panic verifies the supervisor-to-intake path.
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

	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	detachSupervisor(cmd)
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
	_ = waitForVictim(t, cmd, 15*time.Second)

	// The supervisor must deliver the report before it exits.
	select {
	case body := <-received:
		assertCrashReport(t, body, "panic", "e2e test crash")

	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for crash report from supervisor")
	}
}

// TestE2ECrashReport_ManyGoroutines exercises the real diagnostics and
// upload path with more goroutines than the report cap.
func TestE2ECrashReport_ManyGoroutines(t *testing.T) {
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

	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"=panic-many-goroutines",
		"DD_TRACE_AGENT_URL="+srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
		"GOTRACEBACK=all",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn crash victim: %v", err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)

	select {
	case body := <-received:
		report := assertRFC0013Body(t, body)
		errObj := report["error"].(map[string]any)
		threads, _ := errObj["threads"].([]any)
		// maxReportThreads=100 is the exact cap; the victim's own goroutines
		// (150) plus runtime/testing-internal ones comfortably exceed it, so
		// this proves truncation engaged rather than merely "some threads
		// arrived".
		if len(threads) != 100 {
			t.Errorf("error.threads length = %d, want exactly 100 (the maxReportThreads cap)", len(threads))
		}

	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for crash report from supervisor")
	}
}

func TestSupervisorLargeCrashDump(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCanonicalAgentRequest(t, r)
		assertCrashReport(t, decompressGzipBody(t, r), "panic", "e2e many-goroutines crash")
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()), e2eRoleEnv+"=panic-large-dump",
		"DD_TRACE_AGENT_URL="+srv.URL, "DD_CRASHTRACKING_ENABLED=true",
		"GOTRACEBACK=all", "GORACE=atexit_sleep_ms=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)
	if stderr.Len() <= 64*1024 {
		t.Fatalf("crash output = %d bytes, want more than 64 KiB", stderr.Len())
	}
	select {
	case <-received:
		t.Logf("supervisor delivered a report from %d bytes of crash output", stderr.Len())
	default:
		t.Fatal("large crash dump did not reach the intake")
	}
}

// TestE2ECrashReport_PanicWithOptions verifies programmatic configuration,
// including the HTTP client, through the public Start path.
func TestE2ECrashReport_PanicWithOptions(t *testing.T) {
	t.Parallel()

	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Crashtracker-Options") != "custom-client" {
			t.Error("supervisor did not use WithHTTPClient")
		}
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
	detachSupervisor(cmd)
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
	_ = waitForVictim(t, cmd, 15*time.Second)

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
		t.Fatal("timeout waiting for crash report; supervisor may not have applied options")
	}
}

// TestE2ECrashReport_CleanExit verifies that a clean process exit does NOT
// produce a crash report.
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
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"=clean",
		"DD_TRACE_AGENT_URL="+srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn crash victim: %v", err)
	}
	if err := waitForVictim(t, cmd, 15*time.Second); err != nil {
		// Clean exit should succeed.
		t.Fatalf("crash victim exited non-zero: %v", err)
	}

	select {
	case <-posted:
		t.Error("crash report was posted on clean exit; want none")
	case <-time.After(2 * time.Second):
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
	uuidVal, ok := ddTagValue(ddtags, "uuid")
	if !ok {
		t.Errorf("ddtags = %q, want a uuid entry", ddtags)
	} else if _, err := uuid.Parse(uuidVal); err != nil {
		t.Errorf("ddtags uuid = %q, not a valid UUID: %v", uuidVal, err)
	}
	incompleteVal, ok := ddTagValue(ddtags, "incomplete")
	if !ok {
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

	// A report is permitted to have no primary stack at all (crashingThread
	// returns nil for a dump with zero parsed threads) or a present-but-empty
	// one (reportIncomplete's Stack.Incomplete case, when parseFrames bails
	// before capturing a single frame): asserting non-empty frames
	// unconditionally would fail on a shape production is allowed to emit.
	// Only assert format/frames when the stack is both present and complete.
	if stack, ok := errObj["stack"].(map[string]any); ok && incompleteVal != "true" {
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
			// DD-API-KEY (hyphenated) is a registered alias internal/env also
			// resolves DD_API_KEY from (see supported_configurations.gen.go),
			// so it needs stripping too or it would survive this filter intact.
			strings.HasPrefix(kv, "DD_API_KEY=") ||
			strings.HasPrefix(kv, "DD-API-KEY=") ||
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

// TestFilterE2EEnvStripsHyphenatedAPIKeyAlias proves the fix for the bug
// where DD-API-KEY (the hyphenated alias internal/env also resolves
// DD_API_KEY from) survived filtering because only the canonical
// underscored name was checked.
func TestFilterE2EEnvStripsHyphenatedAPIKeyAlias(t *testing.T) {
	in := []string{"PATH=/usr/bin", "DD-API-KEY=dummy"}
	out := filterE2EEnv(in)

	for _, kv := range out {
		if strings.HasPrefix(kv, "DD-API-KEY=") {
			t.Errorf("filterE2EEnv(%v) = %v, want DD-API-KEY stripped", in, out)
		}
	}
}

// waitForVictim waits for the crash-victim subprocess to exit, bounded by
// timeout. Without this bound, a victim that hangs during startup (before
// ever reaching Start or the deliberate crash) would block here indefinitely,
// so a test's own "wait for the report" timeout below would never even begin:
// the two waits are sequential, not one deadline covering both. On timeout
// the victim is killed and reaped so the failure is reported promptly rather
// than surfacing later as an outer go test -timeout or CI job timeout.
func waitForVictim(t *testing.T, cmd *exec.Cmd, timeout time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done // reap
		t.Fatalf("timeout waiting for crash victim (pid %d) to exit", cmd.Process.Pid)
	}
	return nil
}
