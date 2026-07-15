// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

	// Content-Type header.
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// API key must NOT be set on the agent path.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "" {
		t.Errorf("DD-API-KEY should be absent on agent path, got %q", key)
	}

	assertRFC0013Body(t, capturedBody)
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

	assertRFC0013Body(t, capturedBody)
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
