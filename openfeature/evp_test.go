// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func TestEVPClientPostRawRequest(t *testing.T) {
	body := []byte(`{"context":{"service":"test-service"},"flagEvaluations":[]}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: got %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != flagEvalLoggingEndpoint {
			t.Errorf("unexpected path: got %q, want %q", r.URL.Path, flagEvalLoggingEndpoint)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("unexpected Content-Type header: got %q, want %q", got, "application/json")
		}
		if got := r.Header.Get(evpSubdomainHeader); got != evpSubdomainValue {
			t.Errorf("unexpected %s header: got %q, want %q", evpSubdomainHeader, got, evpSubdomainValue)
		}
		if got := r.Header.Get(headerEVPOrigin); got != evpOrigin {
			t.Errorf("unexpected %s header: got %q, want %q", headerEVPOrigin, got, evpOrigin)
		}
		if got := r.Header.Get(headerEVPOriginVersion); got != version.Tag {
			t.Errorf("unexpected %s header: got %q, want %q", headerEVPOriginVersion, got, version.Tag)
		}

		gotBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		} else if !bytes.Equal(gotBody, body) {
			t.Errorf("unexpected body: got %q, want %q", gotBody, body)
		}

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	agentURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	client := &evpClient{
		httpClient: server.Client(),
		agentURL:   agentURL,
	}
	if err := client.postRaw(flagEvalLoggingEndpoint, "flag evaluation", body); err != nil {
		t.Fatalf("postRaw returned an error: %v", err)
	}
}
