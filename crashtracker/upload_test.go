// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decompressBody gunzips a captured request body for JSON assertions.
func decompressBody(t *testing.T, body []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("create gzip reader: %v", err)
	}
	defer r.Close()
	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip stream: %v", err)
	}
	return decompressed
}

func newTestReport() *Report {
	stack := StackTrace{
		Format: "Datadog Crashtracker 1.0",
		Frames: []Frame{{
			Function: "main.main",
			File:     "main.go",
			Line:     10,
		}},
	}
	return &Report{
		Timestamp: 1700000000000,
		DDSource:  "crashtracker",
		DDTags:    "language:go",
		Error: Error{
			Type:       "SIGSEGV",
			Message:    "segmentation fault",
			Stack:      &stack,
			Threads:    []Thread{{Crashed: true, Name: "goroutine 1", Stack: stack}},
			ThreadName: "goroutine 1",
			IsCrash:    true,
			SourceType: "Crashtracking",
		},
		OSInfo: OSInfo{Architecture: "amd64", Bitness: "64-bit"},
	}
}

func TestUploadReportAgentPath(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		capturedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	if err := uploadReport(cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("no request captured by test server")
	}

	assertCanonicalAgentRequest(t, capturedReq)

	// Content-Type and Content-Encoding headers.
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if ce := capturedReq.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", ce)
	}

	// API key must NOT be set on the agent path.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "" {
		t.Errorf("DD-API-KEY should be absent on agent path, got %q", key)
	}

	assertRFC0013Body(t, decompressBody(t, capturedBody))
}

func TestUploadReportAgentlessPath(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		capturedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		apiKey:       "test-key",
		site:         "datadoghq.com",
		agentlessURL: srv.URL, // test-only override for the agentless target
		httpClient:   srv.Client(),
	}

	if err := uploadReport(cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("no request captured by test server")
	}

	// DD-API-KEY header must be set.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "test-key" {
		t.Errorf("DD-API-KEY = %q, want test-key", key)
	}

	// EVP subdomain must NOT be set on agentless path.
	if sub := capturedReq.Header.Get("X-Datadog-EVP-Subdomain"); sub != "" {
		t.Errorf("X-Datadog-EVP-Subdomain should be absent on agentless path, got %q", sub)
	}

	assertRFC0013Body(t, decompressBody(t, capturedBody))
}

// TestUploadReportAgentURLIgnoredInAgentlessMode verifies the fix for the bug
// where WithAgentURL was read verbatim as the complete agentless target: a
// resolved cfg.agentURL (an agent host:port, no path) combined with an API
// key must not become the agentless request's target URL, which would 404 or
// otherwise fail to reach the intake path. The computed agentless URL is used
// instead; only the unexported test seam (agentlessURL) can override it.
func TestUploadReportAgentURLIgnoredInAgentlessMode(t *testing.T) {
	cfg := &config{
		apiKey:   "test-key",
		site:     "datadoghq.com",
		agentURL: "http://agent-host:8126", // must be ignored: this is an agent base, not an agentless target
	}

	req, _, err := buildRequestAndClient(cfg, []byte("{}"))
	if err != nil {
		t.Fatalf("buildRequestAndClient returned unexpected error: %v", err)
	}

	if req.URL.String() == cfg.agentURL {
		t.Fatalf("target URL = %q, want the computed agentless URL, not the agent URL verbatim", req.URL.String())
	}
	wantHost := "error-tracking-intake.datadoghq.com"
	if req.URL.Host != wantHost {
		t.Errorf("target host = %q, want %q", req.URL.Host, wantHost)
	}
}

func TestUploadReportServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status code 500", err.Error())
	}
}

// TestUploadReportRejects3xx verifies the fix for the bug where any status
// below 400 — including a 3xx client.Do did not or could not follow — was
// treated as a successful upload. 304 is a clean case to test: it is not in
// net/http's default set of client-followed redirect codes, so client.Do
// returns it as-is with err == nil, exactly the shape a misbehaving proxy or
// agent could produce.
func TestUploadReportRejects3xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 304 response, got nil")
	}
	if !strings.Contains(err.Error(), "304") {
		t.Errorf("error %q does not mention status code 304", err.Error())
	}
}

// TestUploadReportRetriesTransientFailure proves a crash report is no longer
// lost to a single transient failure: the intake returning 503 (Agent
// mid-restart) on the first attempt and 202 on the second must still result
// in a delivered report, not a permanently lost one.
func TestUploadReportRetriesTransientFailure(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	if err := uploadReport(cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("requestCount = %d, want 2 (one failure, one success)", requestCount)
	}
}

// TestUploadReportDoesNotRetryBadRequest proves a non-retryable 4xx (which
// will fail identically on every attempt) does not waste the monitor's
// remaining time before exit retrying a request that can never succeed.
func TestUploadReportDoesNotRetryBadRequest(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1 (400 must not be retried)", requestCount)
	}
}

// TestUploadReportGivesUpAfterAllAttemptsFail proves uploadReport eventually
// stops rather than retrying forever, and that the final error is
// informative once every attempt has failed.
func TestUploadReportGivesUpAfterAllAttemptsFail(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error after every attempt fails, got nil")
	}
	if requestCount != uploadAttempts {
		t.Errorf("requestCount = %d, want %d", requestCount, uploadAttempts)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error %q does not mention the underlying status code 503", err.Error())
	}
}

// TestBuildRequestToleratesDDSiteAsURL verifies the fix for DD_SITE supplied
// as a full URL ("https://datadoghq.com") rather than a bare host, which
// would otherwise interpolate into a malformed intake URL.
func TestBuildRequestToleratesDDSiteAsURL(t *testing.T) {
	cfg := &config{
		apiKey: "test-key",
		site:   "https://datadoghq.com/",
	}

	req, _, err := buildRequestAndClient(cfg, []byte("{}"))
	if err != nil {
		t.Fatalf("buildRequestAndClient returned unexpected error: %v", err)
	}

	wantHost := "error-tracking-intake.datadoghq.com"
	if req.URL.Host != wantHost {
		t.Errorf("target host = %q, want %q", req.URL.Host, wantHost)
	}
}
