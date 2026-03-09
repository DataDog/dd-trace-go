// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// assertContentLength verifies that the Content-Length header is present and
// matches the actual body length written by the handler.
func assertContentLength(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	cl := recorder.Header().Get("Content-Length")
	if cl == "" {
		t.Error("Content-Length header is missing")
		return
	}
	expected := strconv.Itoa(recorder.Body.Len())
	if cl != expected {
		t.Errorf("Content-Length mismatch: header=%q, body length=%q", cl, expected)
	}
}

func TestBlockRequestHandler_ContentLength(t *testing.T) {
	tests := []struct {
		name               string
		contentType        string
		payload            []byte
		securityResponseID string
	}{
		{
			name:               "json template",
			contentType:        "application/json",
			payload:            blockedTemplateJSON,
			securityResponseID: "test-response-id",
		},
		{
			name:               "html template",
			contentType:        "text/html",
			payload:            blockedTemplateHTML,
			securityResponseID: "test-response-id",
		},
		{
			name:               "empty security response ID",
			contentType:        "application/json",
			payload:            blockedTemplateJSON,
			securityResponseID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newBlockRequestHandler(403, tc.contentType, tc.payload, tc.securityResponseID)

			req, err := http.NewRequest(http.MethodGet, "/", nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			if got := recorder.Code; got != 403 {
				t.Errorf("expected status 403, got %d", got)
			}
			if ct := recorder.Header().Get("Content-Type"); ct != tc.contentType {
				t.Errorf("expected Content-Type %q, got %q", tc.contentType, ct)
			}
			assertContentLength(t, recorder)
		})
	}
}

func TestBlockHandler_AutoDetect_ContentLength(t *testing.T) {
	tests := []struct {
		name            string
		acceptHeader    string
		wantContentType string
	}{
		{
			name:            "no Accept header defaults to json",
			acceptHeader:    "",
			wantContentType: "application/json",
		},
		{
			name:            "Accept application/json",
			acceptHeader:    "application/json",
			wantContentType: "application/json",
		},
		{
			name:            "Accept text/html",
			acceptHeader:    "text/html",
			wantContentType: "text/html",
		},
		{
			name:            "Accept text/html before application/json",
			acceptHeader:    "text/html, application/json",
			wantContentType: "text/html",
		},
		{
			name:            "Accept application/json before text/html",
			acceptHeader:    "application/json, text/html",
			wantContentType: "application/json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newBlockHandler(403, "auto", "test-id")

			req, err := http.NewRequest(http.MethodGet, "/", nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			if tc.acceptHeader != "" {
				req.Header.Set("Accept", tc.acceptHeader)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			if got := recorder.Code; got != 403 {
				t.Errorf("expected status 403, got %d", got)
			}
			if ct := recorder.Header().Get("Content-Type"); ct != tc.wantContentType {
				t.Errorf("expected Content-Type %q, got %q", tc.wantContentType, ct)
			}
			assertContentLength(t, recorder)
		})
	}
}
