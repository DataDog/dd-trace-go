// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	JobArtifactSchemaVersion = "1"
	MaxJobArtifactBytes      = 16 * 1024 * 1024
)

// JobArtifact is the only cross-job control document. The digest covers all
// fields except itself, including the canonical payload and prior-artifact link.
type JobArtifact struct {
	SchemaVersion   string           `json:"schema_version"`
	Job             OrchestrationJob `json:"job"`
	RequestKey      string           `json:"request_key"`
	WorkflowRunID   string           `json:"workflow_run_id"`
	WorkflowAttempt int              `json:"workflow_run_attempt"`
	WorkflowSHA     string           `json:"workflow_sha"`
	PreviousSHA256  string           `json:"previous_sha256,omitempty"`
	Payload         json.RawMessage  `json:"payload"`
	ArtifactSHA256  string           `json:"artifact_sha256"`
}

type unsignedJobArtifact struct {
	SchemaVersion   string           `json:"schema_version"`
	Job             OrchestrationJob `json:"job"`
	RequestKey      string           `json:"request_key"`
	WorkflowRunID   string           `json:"workflow_run_id"`
	WorkflowAttempt int              `json:"workflow_run_attempt"`
	WorkflowSHA     string           `json:"workflow_sha"`
	PreviousSHA256  string           `json:"previous_sha256,omitempty"`
	Payload         json.RawMessage  `json:"payload"`
}

var jobPayloadFields = map[OrchestrationJob]map[string]bool{
	JobValidateRequest:   {"request": true, "validated_request": true, "selector": true},
	JobReserveOperation:  {"record": true, "state_head": true, "decision": true},
	JobGenerateUnsigned:  {"record": true, "generation": true, "manifest": true, "repository_bundle_sha256": true},
	JobValidateSignStore: {"record": true, "state_head": true, "signed_output": true},
	JobPublishBranches:   {"record": true, "state_head": true, "publication": true},
	JobWaitBranchTests:   {"record": true, "state_head": true, "test_evidence": true},
	JobPublishTags:       {"record": true, "state_head": true, "publication": true},
	JobEnsurePreparePR:   {"record": true, "state_head": true, "pr_number": true, "outcome": true},
	JobObserveImages:     {"record": true, "state_head": true, "image_evidence": true, "outcome": true},
	JobRecordOutcome:     {"record": true, "state_head": true, "outcome": true},
	JobFeedback:          {"record": true, "feedback": true, "outcome": true},
}

// NewJobArtifact creates a canonical, digest-linked cross-job document.
func NewJobArtifact(job OrchestrationJob, requestKey, runID string, attempt int, workflowSHA, previousSHA string, payload json.RawMessage) (JobArtifact, error) {
	artifact := JobArtifact{SchemaVersion: JobArtifactSchemaVersion, Job: job, RequestKey: requestKey, WorkflowRunID: runID, WorkflowAttempt: attempt, WorkflowSHA: workflowSHA, PreviousSHA256: previousSHA}
	canonicalPayload, err := validateJobPayload(job, payload)
	if err != nil {
		return JobArtifact{}, err
	}
	artifact.Payload = canonicalPayload
	if err := validateJobArtifactMetadata(artifact); err != nil {
		return JobArtifact{}, err
	}
	digest, err := jobArtifactDigest(artifact)
	if err != nil {
		return JobArtifact{}, err
	}
	artifact.ArtifactSHA256 = digest
	return artifact, nil
}

// DecodeJobArtifact rejects duplicate, unknown, null, trailing, oversized, and
// non-canonical control input before a job uses any payload field.
func DecodeJobArtifact(raw []byte, expectedJob OrchestrationJob) (JobArtifact, error) {
	if err := validateJSONNoDuplicateKeys(raw, MaxJobArtifactBytes); err != nil {
		return JobArtifact{}, err
	}
	fields, err := decodeStrictObject(raw, MaxJobArtifactBytes, typedContractError("invalid_job_artifact"), typedContractError("trailing_job_artifact"), typedContractError("duplicate_job_artifact_key"))
	if err != nil {
		return JobArtifact{}, typedContractError("invalid_job_artifact")
	}
	allowed := map[string]bool{"schema_version": true, "job": true, "request_key": true, "workflow_run_id": true, "workflow_run_attempt": true, "workflow_sha": true, "previous_sha256": true, "payload": true, "artifact_sha256": true}
	for key := range fields {
		if !allowed[key] {
			return JobArtifact{}, typedContractError("unknown_job_artifact_key")
		}
	}
	var artifact JobArtifact
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return JobArtifact{}, typedContractError("invalid_job_artifact")
	}
	if artifact.Job != expectedJob {
		return JobArtifact{}, newReleaseError(ErrorClassStateConflict, "job_artifact_phase_mismatch")
	}
	payload, err := validateJobPayload(artifact.Job, artifact.Payload)
	if err != nil {
		return JobArtifact{}, err
	}
	artifact.Payload = payload
	if err := validateJobArtifactMetadata(artifact); err != nil {
		return JobArtifact{}, err
	}
	digest, err := jobArtifactDigest(artifact)
	if err != nil {
		return JobArtifact{}, err
	}
	if artifact.ArtifactSHA256 != digest {
		return JobArtifact{}, newReleaseError(ErrorClassStateConflict, "job_artifact_digest_mismatch")
	}
	return artifact, nil
}

func validateJobArtifactMetadata(artifact JobArtifact) error {
	if artifact.SchemaVersion != JobArtifactSchemaVersion || !validRequestKey(artifact.RequestKey) || !validID(artifact.WorkflowRunID) || artifact.WorkflowAttempt <= 0 || !ValidGitObjectID(artifact.WorkflowSHA) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_job_artifact_metadata")
	}
	if artifact.PreviousSHA256 != "" && !lowerHexDigest(artifact.PreviousSHA256) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_job_artifact_metadata")
	}
	if artifact.ArtifactSHA256 != "" && !lowerHexDigest(artifact.ArtifactSHA256) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_job_artifact_metadata")
	}
	if _, ok := jobPayloadFields[artifact.Job]; !ok {
		return newReleaseError(ErrorClassContractMismatch, "invalid_job_artifact_job")
	}
	return nil
}

func validateJobPayload(job OrchestrationJob, raw json.RawMessage) (json.RawMessage, error) {
	allowed, ok := jobPayloadFields[job]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, typedContractError("invalid_job_payload")
	}
	if err := validateJSONNoDuplicateKeys(raw, MaxJobArtifactBytes); err != nil {
		return nil, err
	}
	fields, err := decodeStrictObject(raw, MaxJobArtifactBytes, typedContractError("invalid_job_payload"), typedContractError("trailing_job_payload"), typedContractError("duplicate_job_payload_key"))
	if err != nil {
		return nil, typedContractError("invalid_job_payload")
	}
	if len(fields) == 0 {
		return nil, typedContractError("invalid_job_payload")
	}
	for key := range fields {
		if !allowed[key] {
			return nil, typedContractError("unknown_job_payload_key")
		}
	}
	for key := range allowed {
		value, found := fields[key]
		if !found || len(value) == 0 || string(value) == "null" {
			return nil, typedContractError("missing_job_payload_key")
		}
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return nil, typedContractError("invalid_job_payload")
	}
	canonical, err := marshalCanonical(generic)
	if err != nil {
		return nil, typedContractError("invalid_job_payload")
	}
	return canonical, nil
}

func jobArtifactDigest(artifact JobArtifact) (string, error) {
	unsigned := unsignedJobArtifact{SchemaVersion: artifact.SchemaVersion, Job: artifact.Job, RequestKey: artifact.RequestKey, WorkflowRunID: artifact.WorkflowRunID, WorkflowAttempt: artifact.WorkflowAttempt, WorkflowSHA: artifact.WorkflowSHA, PreviousSHA256: artifact.PreviousSHA256, Payload: artifact.Payload}
	canonical, err := canonicalJSON(unsigned)
	if err != nil {
		return "", wrapReleaseError(ErrorClassContractMismatch, "job_artifact_digest_failed", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func validRequestKey(value string) bool {
	for index, part := range bytes.Split([]byte(value), []byte{':'}) {
		if index > 1 || !validID(string(part)) {
			return false
		}
	}
	parts := bytes.Count([]byte(value), []byte{':'})
	return parts == 1
}

// validateJSONNoDuplicateKeys scans every nested object. encoding/json only
// reports duplicate keys when callers inspect tokens, so unmarshalling alone
// cannot protect a signed control document.
func validateJSONNoDuplicateKeys(raw []byte, maxBytes int) error {
	if len(raw) == 0 || len(raw) > maxBytes || !utf8.Valid(raw) {
		return typedContractError("invalid_job_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return typedContractError("invalid_job_json")
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return typedContractError("trailing_job_json")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("null JSON value")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate or invalid object key")
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid array")
		}
	default:
		return fmt.Errorf("invalid delimiter")
	}
	return nil
}

// DecodeJobPayload decodes a previously validated payload into a fixed type.
func DecodeJobPayload(artifact JobArtifact, target any) error {
	if target == nil {
		return typedContractError("invalid_job_payload")
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return typedContractError("invalid_job_payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return typedContractError("trailing_job_payload")
	}
	return nil
}

// JobArtifactName is fixed from the trusted job identifier.
func JobArtifactName(job OrchestrationJob) (string, error) {
	if _, ok := jobPayloadFields[job]; !ok {
		return "", newReleaseError(ErrorClassContractMismatch, "invalid_job_artifact_job")
	}
	return fmt.Sprintf("gardener-release-%s-v1.json", job), nil
}
