// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestReport() *Report {
	return &Report{
		Timestamp: 1700000000000,
		DDSource:  "crashtracker",
		Error: Error{
			Type:    "SIGSEGV",
			Message: "segmentation fault",
			IsCrash: true,
		},
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

	// Path must end with the EVP proxy path.
	if !strings.HasSuffix(capturedReq.URL.Path, "/evp_proxy/v2/api/v2/errors") {
		t.Errorf("unexpected path: %q, want suffix /evp_proxy/v2/api/v2/errors", capturedReq.URL.Path)
	}

	// Content-Type header.
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// EVP subdomain header.
	if sub := capturedReq.Header.Get("X-Datadog-EVP-Subdomain"); sub != "errorsintake.agent" {
		t.Errorf("X-Datadog-EVP-Subdomain = %q, want errorsintake.agent", sub)
	}

	// API key must NOT be set on the agent path.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "" {
		t.Errorf("DD-API-KEY should be absent on agent path, got %q", key)
	}

	// Body must be valid JSON with expected fields.
	var got map[string]any
	if err := json.Unmarshal(capturedBody, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if got["ddsource"] != "crashtracker" {
		t.Errorf("ddsource = %v, want crashtracker", got["ddsource"])
	}
	errObj, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or wrong type: %T", got["error"])
	}
	if errObj["is_crash"] != true {
		t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
	}
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
		apiKey:     "test-key",
		site:       "datadoghq.com",
		agentURL:   srv.URL, // redirect agentless URL to test server
		httpClient: srv.Client(),
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

	// Body must be valid JSON.
	var got map[string]any
	if err := json.Unmarshal(capturedBody, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if got["ddsource"] != "crashtracker" {
		t.Errorf("ddsource = %v, want crashtracker", got["ddsource"])
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
