// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"strings"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

type cliFS map[string][]byte

func (f cliFS) ReadFile(name string) ([]byte, error) {
	return append([]byte(nil), f[name]...), nil
}

func TestInspectRequestCommandPrintsSanitizedEvidence(t *testing.T) {
	policy := []byte(`{"schema_version":"1","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","state_branch":"gardener-release-state","release_concurrency_group":"gardener-release-production-v1","issue_mapping":{"456":"v2.11"},"limits":{"api_max_pages":200,"api_page_size":100,"api_response_bytes":16777216,"read_retries":3,"polling_deadline_seconds":1800}}`)
	context := `{"repository_id":"123","repository_full_name":"DataDog/dd-trace-go","issue_number":"456","original_comment_id":"789","acknowledgement_comment_id":"790","body_snapshot":"/gardener release:promote v2.11","policy_revision":"` + gardenerrelease.PolicyRevision(policy) + `"}`
	input := []byte(`{"contract_version":"1","command":"release:promote","version":"v2.11.0","context":"` + strings.ReplaceAll(context, `"`, `\"`) + `"}`)
	files := cliFS{"input.json": input, "policy.json": policy}
	var stdout, stderr bytes.Buffer
	code := run([]string{"inspect-request", "--input", "input.json", "--policy", "policy.json"}, files, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{`"ok": true`, `"request_key": "123:789"`, `"release_line": "v2.11"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %s: %s", want, text)
		}
	}
	for _, forbidden := range []string{"Authorization", "Bearer", "private", "secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("output contains forbidden token %q: %s", forbidden, text)
		}
	}
}

func TestUnknownCommandReturnsTypedError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"mutate-release"}, cliFS{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `"error":"unknown_command"`) || !strings.Contains(stderr.String(), `"class":"contract_mismatch"`) {
		t.Fatalf("stderr = %s", stderr.String())
	}
}
