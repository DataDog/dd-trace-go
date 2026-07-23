// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"encoding/json"
	"net/http"
	"testing"
)

func assertCanonicalAgentRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Path != "/evp_proxy/v4/api/v2/errorsintake" {
		t.Errorf("path = %q, want /evp_proxy/v4/api/v2/errorsintake", r.URL.Path)
	}
	if got := r.Header.Get("X-Datadog-EVP-Subdomain"); got != "error-tracking-intake" {
		t.Errorf("EVP subdomain = %q, want error-tracking-intake", got)
	}
}

func assertRFC0013Body(t *testing.T, body []byte) {
	t.Helper()

	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, body)
	}
	if report["ddsource"] != "crashtracker" {
		t.Errorf("ddsource = %v, want crashtracker", report["ddsource"])
	}
	if _, ok := report["timestamp"].(float64); !ok {
		t.Errorf("timestamp type = %T, want number", report["timestamp"])
	}
	if ddtags, _ := report["ddtags"].(string); ddtags == "" {
		t.Error("ddtags is empty")
	}

	errObj, ok := report["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or wrong type: %T", report["error"])
	}
	if errObj["is_crash"] != true {
		t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
	}
	if errObj["source_type"] != "Crashtracking" {
		t.Errorf("error.source_type = %v, want Crashtracking", errObj["source_type"])
	}
	if _, ok := errObj["type"]; !ok {
		t.Error("error.type missing")
	}
	stack, ok := errObj["stack"].(map[string]any)
	if !ok {
		t.Error("error.stack missing or wrong type")
	} else {
		if stack["format"] != "Datadog Crashtracker 1.0" {
			t.Errorf("error.stack.format = %v, want Datadog Crashtracker 1.0", stack["format"])
		}
		if frames, _ := stack["frames"].([]any); len(frames) == 0 {
			t.Error("error.stack.frames is empty")
		}
	}
	if threads, _ := errObj["threads"].([]any); len(threads) == 0 {
		t.Error("error.threads is empty")
	}

	osInfo, ok := report["os_info"].(map[string]any)
	if !ok {
		t.Fatal("os_info missing or wrong type")
	}
	if architecture, _ := osInfo["architecture"].(string); architecture == "" {
		t.Error("os_info.architecture is empty")
	}
}
