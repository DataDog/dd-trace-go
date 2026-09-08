// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
	"testing"
)

func validPolicyJSON() []byte {
	return []byte(`{"schema_version":"1","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","state_branch":"gardener-release-state","release_concurrency_group":"gardener-release-production-v1","issue_mapping":{"456":"v2.11"},"limits":{"api_max_pages":200,"api_page_size":100,"api_response_bytes":16777216,"read_retries":3,"polling_deadline_seconds":1800},"test_policy":{"workflow_id":"","workflow_path":".github/workflows/main-branch-tests.yml","workflow_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","event":"push","required_jobs":["release-tests-complete"],"deadline_seconds":1800}}`)
}

func validDispatchJSON(policyRaw []byte) []byte {
	context := `{"repository_id":"123","repository_full_name":"DataDog/dd-trace-go","issue_number":"456","original_comment_id":"789","acknowledgement_comment_id":"790","body_snapshot":"/gardener release:promote v2.11","policy_revision":"` + PolicyRevision(policyRaw) + `"}`
	return []byte(`{"contract_version":"1","command":"release:promote","version":"v2.11.0","context":` + quoted(context) + `}`)
}

func quoted(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func TestInspectRequestValidatesPolicyRevisionAndIssueMapping(t *testing.T) {
	policyRaw := validPolicyJSON()
	inspection, err := InspectRequest(validDispatchJSON(policyRaw), policyRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.OK || inspection.RequestKey != "123:789" || inspection.ReleaseLine != "v2.11" || inspection.PolicyRevision != PolicyRevision(policyRaw) {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
}

func TestDecodeDispatchJSONStrictFailures(t *testing.T) {
	policyRaw := validPolicyJSON()
	validInput := string(validDispatchJSON(policyRaw))
	cases := []struct {
		name string
		raw  []byte
		code string
	}{
		{name: "top level duplicate", raw: []byte(strings.Replace(validInput, `"contract_version":"1"`, `"contract_version":"1","contract_version":"1"`, 1)), code: "duplicate_context_key"},
		{name: "top level array", raw: []byte(`[]`), code: "wrong_context_type"},
		{name: "trailing json", raw: []byte(validInput + `{}`), code: "trailing_context_json"},
		{name: "invalid utf8", raw: []byte{0xff, 0xfe}, code: "invalid_context_json"},
		{name: "unknown key", raw: []byte(strings.Replace(validInput, `"context"`, `"unexpected":"x","context"`, 1)), code: "unknown_input_key"},
		{name: "wrong type", raw: []byte(strings.Replace(validInput, `"command":"release:promote"`, `"command":null`, 1)), code: "wrong_context_type"},
		{name: "context null field", raw: []byte(strings.Replace(validInput, `\"body_snapshot\":\"/gardener release:promote v2.11\"`, `\"body_snapshot\":null`, 1)), code: "wrong_context_type"},
		{name: "oversized", raw: []byte(`{"contract_version":"1","command":"release:promote","version":"v2.11.0","context":"` + strings.Repeat("a", MaxContextBytes+1) + `"}`), code: "invalid_context_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeDispatchJSON(tc.raw)
			if ErrorCode(err) != tc.code {
				t.Fatalf("error = %q, want %q", ErrorCode(err), tc.code)
			}
		})
	}
}

func TestDecodePolicyStrictFailures(t *testing.T) {
	validPolicy := string(validPolicyJSON())
	cases := []struct {
		name string
		raw  []byte
		code string
	}{
		{name: "duplicate key", raw: []byte(strings.Replace(validPolicy, `"schema_version":"1"`, `"schema_version":"1","schema_version":"1"`, 1)), code: "duplicate_policy_key"},
		{name: "unknown key", raw: []byte(strings.Replace(validPolicy, `"limits"`, `"unexpected":"x","limits"`, 1)), code: "unknown_policy_key"},
		{name: "wrong type", raw: []byte(strings.Replace(validPolicy, `"repository_id":"123"`, `"repository_id":123`, 1)), code: "wrong_policy_type"},
		{name: "null issue mapping", raw: []byte(strings.Replace(validPolicy, `"456":"v2.11"`, `"456":null`, 1)), code: "wrong_policy_type"},
		{name: "null limit", raw: []byte(strings.Replace(validPolicy, `"api_page_size":100`, `"api_page_size":null`, 1)), code: "wrong_policy_type"},
		{name: "unsafe id", raw: []byte(strings.Replace(validPolicy, `"repository_id":"123"`, `"repository_id":"0123"`, 1)), code: "unsafe_id"},
		{name: "drifted repository", raw: []byte(strings.Replace(validPolicy, RepositoryFullName, "example.com/private", 1)), code: "invalid_repository"},
		{name: "limit exceeded", raw: []byte(strings.Replace(validPolicy, `"read_retries":3`, `"read_retries":4`, 1)), code: "policy_limit_exceeded"},
		{name: "missing test policy", raw: []byte(strings.Replace(validPolicy, `,"test_policy":{"workflow_id":"","workflow_path":".github/workflows/main-branch-tests.yml","workflow_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","event":"push","required_jobs":["release-tests-complete"],"deadline_seconds":1800}`, ``, 1)), code: "missing_policy_key"},
		{name: "null required jobs", raw: []byte(strings.Replace(validPolicy, `"required_jobs":["release-tests-complete"]`, `"required_jobs":null`, 1)), code: "wrong_policy_type"},
		{name: "unknown test policy key", raw: []byte(strings.Replace(validPolicy, `"event":"push"`, `"bypass":true,"event":"push"`, 1)), code: "unknown_policy_key"},
		{name: "wrong test workflow", raw: []byte(strings.Replace(validPolicy, `.github/workflows/main-branch-tests.yml`, `.github/workflows/other.yml`, 1)), code: "policy_drift"},
		{name: "test deadline exceeded", raw: []byte(strings.Replace(validPolicy, `"deadline_seconds":1800`, `"deadline_seconds":21601`, 1)), code: "policy_drift"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodePolicy(tc.raw)
			if ErrorCode(err) != tc.code {
				t.Fatalf("error = %q, want %q", ErrorCode(err), tc.code)
			}
		})
	}
}

func TestValidateRequestAgainstPolicyRechecksFetchedSource(t *testing.T) {
	policyRaw := validPolicyJSON()
	policy, err := DecodePolicy(policyRaw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeDispatchJSON(validDispatchJSON(policyRaw))
	if err != nil {
		t.Fatal(err)
	}
	source := OriginalComment{
		RepositoryID:       "123",
		RepositoryFullName: RepositoryFullName,
		IssueNumber:        "456",
		CommentID:          "789",
		Body:               "/gardener release:promote v2.11",
		AuthorAssociation:  "MEMBER",
	}
	validated, err := ValidateRequestAgainstPolicy(request, policy, source)
	if err != nil {
		t.Fatal(err)
	}
	if validated.ReleaseLine != "v2.11" || validated.RequestSHA256 == "" {
		t.Fatalf("unexpected validation: %#v", validated)
	}
	source.AuthorAssociation = "CONTRIBUTOR"
	_, err = ValidateRequestAgainstPolicy(request, policy, source)
	if ErrorCode(err) != "unauthorized_actor" || ClassOf(err) != ErrorClassRequestRejected {
		t.Fatalf("error = %q/%q, want unauthorized request rejection", ClassOf(err), ErrorCode(err))
	}
	source.AuthorAssociation = "MEMBER"
	source.Body = "/gardener release:promote v2.12"
	_, err = ValidateRequestAgainstPolicy(request, policy, source)
	if ErrorCode(err) != "source_mismatch" {
		t.Fatalf("error = %q, want source_mismatch", ErrorCode(err))
	}
}

func TestInspectOperationStrictReadOnlyEvidence(t *testing.T) {
	raw := []byte(`{"schema_version":"1","phase":"reserved","request_key":"123:789","request_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","issue_number":"456","original_comment_id":"789","acknowledgement_comment_id":"790","command":"release:promote","requested_version":"v2.11.0","policy_revision":"abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"}`)
	inspection, err := InspectOperation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.OK || inspection.RequestKey != "123:789" {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
	_, err = InspectOperation([]byte(strings.Replace(string(raw), `"phase":"reserved"`, `"phase":"reserved","phase":"complete"`, 1)))
	if ErrorCode(err) != "duplicate_operation_key" {
		t.Fatalf("error = %q, want duplicate_operation_key", ErrorCode(err))
	}
	_, err = InspectOperation([]byte(strings.Replace(string(raw), `"request_key":"123:789"`, `"request_key":"123:999"`, 1)))
	if ErrorCode(err) != "source_mismatch" {
		t.Fatalf("error = %q, want source_mismatch", ErrorCode(err))
	}
}
