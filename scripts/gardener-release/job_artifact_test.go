// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJobArtifactRoundTripAndDigestChain(t *testing.T) {
	payload := json.RawMessage(`{"request":{"contract_version":"1"},"validated_request":{"request_key":"123:789"},"selector":{"record_found":false}}`)
	artifact, err := NewJobArtifact(JobValidateRequest, "123:789", "44", 2, strings.Repeat("a", 40), "", payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJobArtifact(raw, JobValidateRequest)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ArtifactSHA256 != artifact.ArtifactSHA256 {
		t.Fatalf("digest = %s, want %s", decoded.ArtifactSHA256, artifact.ArtifactSHA256)
	}
	name, err := JobArtifactName(JobValidateRequest)
	if err != nil || name != "gardener-release-validate_request-v1.json" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
}

func TestJobArtifactStrictFailures(t *testing.T) {
	payload := json.RawMessage(`{"record":{"phase":"reserved"},"state_head":"` + strings.Repeat("b", 40) + `","decision":{}}`)
	artifact, err := NewJobArtifact(JobReserveOperation, "123:789", "44", 1, strings.Repeat("a", 40), strings.Repeat("c", 64), payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(artifact)
	cases := map[string][]byte{
		"wrong phase":      raw,
		"trailing":         append(append([]byte(nil), raw...), []byte(` {}`)...),
		"unknown outer":    []byte(strings.Replace(string(raw), `"artifact_sha256":`, `"extra":1,"artifact_sha256":`, 1)),
		"duplicate nested": []byte(strings.Replace(string(raw), `"phase":"reserved"`, `"phase":"reserved","phase":"signed"`, 1)),
		"unknown payload":  []byte(strings.Replace(string(raw), `"decision":{}`, `"surprise":{}`, 1)),
		"null payload":     []byte(strings.Replace(string(raw), string(artifact.Payload), `null`, 1)),
		"tampered digest":  []byte(strings.Replace(string(raw), strings.Repeat("b", 40), strings.Repeat("d", 40), 1)),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			expected := JobReserveOperation
			if name == "wrong phase" {
				expected = JobPublishTags
			}
			if _, err := DecodeJobArtifact(input, expected); err == nil {
				t.Fatal("malformed artifact accepted")
			}
		})
	}
	oversized := make([]byte, MaxJobArtifactBytes+1)
	if _, err := DecodeJobArtifact(oversized, JobReserveOperation); err == nil {
		t.Fatal("oversized artifact accepted")
	}
}

func TestEveryWorkflowJobHasFixedArtifactSchema(t *testing.T) {
	for job := range workflowCredentialDomains {
		if _, ok := jobPayloadFields[job]; !ok {
			t.Fatalf("job %s has no payload schema", job)
		}
		if _, err := JobArtifactName(job); err != nil {
			t.Fatalf("job %s: %v", job, err)
		}
	}
}
