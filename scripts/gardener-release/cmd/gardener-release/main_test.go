// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

type cliFS map[string][]byte

func (f cliFS) ReadFile(name string) ([]byte, error) {
	return append([]byte(nil), f[name]...), nil
}

func TestInspectRequestCommandPrintsSanitizedEvidence(t *testing.T) {
	policy := []byte(`{"schema_version":"1","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","state_branch":"gardener-release-state","release_concurrency_group":"gardener-release-production-v1","issue_mapping":{"456":"v2.11"},"limits":{"api_max_pages":200,"api_page_size":100,"api_response_bytes":16777216,"read_retries":3,"polling_deadline_seconds":1800},"test_policy":{"workflow_id":"20","workflow_path":".github/workflows/main-branch-tests.yml","workflow_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","event":"push","required_jobs":["release-tests-complete"],"deadline_seconds":1800},"image_policy":{"workflow_id":"30","workflow_path":".github/workflows/docker-images-release.yml","workflow_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","child_workflow_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},"ssh_signing_policy":{"principal":"gardener-release-fixture","public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMRxzv8rrcMLUlupjDqA/vVikQaxPjT60BRzOn948Pqf","fingerprint":"SHA256:uUWkEC8QiBrOvQdplgmRQxHii6n4kosoK4KccmL1OME"},"gardener_identity":{"author_id":"2001","author_login":"gardener-fixture"}}`)
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

func TestRemainingPhaseCommandsExistAndRejectMissingInputs(t *testing.T) {
	for _, command := range []string{"wait-branch-tests", "publish-tags", "ensure-prepare-pr", "observe-images", "record-outcome", "publish-feedback"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{command}, cliFS{}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `"error":"invalid_arguments"`) || strings.Contains(stderr.String(), "unknown_command") {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
		})
	}
}

func TestPhaseCommandRejectsPredecessorRuntimeDriftBeforeCredentials(t *testing.T) {
	payload := json.RawMessage(`{"record":{"reservation":{"request_key":"123:789"}},"state_head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","publication":{}}`)
	artifact, err := gardenerrelease.NewJobArtifact(gardenerrelease.JobPublishBranches, "123:789", "44", 1, strings.Repeat("a", 40), strings.Repeat("c", 64), payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(artifact)
	t.Setenv("GITHUB_RUN_ID", "45")
	t.Setenv("GITHUB_RUN_ATTEMPT", "1")
	t.Setenv("GITHUB_SHA", strings.Repeat("a", 40))
	var stdout, stderr bytes.Buffer
	code := run([]string{"wait-branch-tests", "--branches", "branches.json", "--policy", "policy.json"}, cliFS{"branches.json": raw}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "job_artifact_runtime_mismatch") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestPhaseCommandRejectsSkippedEmptyPredecessorOutput(t *testing.T) {
	payload := json.RawMessage(`{"record":{"reservation":{"request_key":"123:789"}},"state_head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","publication":{}}`)
	sha := strings.Repeat("a", 40)
	artifact, err := gardenerrelease.NewJobArtifact(gardenerrelease.JobPublishBranches, "123:789", "44", 1, sha, "", payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(artifact)
	t.Setenv("GITHUB_RUN_ID", "44")
	t.Setenv("GITHUB_RUN_ATTEMPT", "1")
	t.Setenv("GITHUB_SHA", sha)
	var stdout, stderr bytes.Buffer
	code := run([]string{"wait-branch-tests", "--branches", "branches.json", "--policy", "policy.json"}, cliFS{"branches.json": raw}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "job_artifact_runtime_mismatch") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
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
