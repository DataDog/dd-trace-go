// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"regexp"
)

type RequestInspection struct {
	OK                       bool   `json:"ok"`
	Command                  string `json:"command"`
	Version                  string `json:"version"`
	RequestKey               string `json:"request_key"`
	RequestSHA256            string `json:"request_sha256"`
	RepositoryID             string `json:"repository_id"`
	RepositoryFullName       string `json:"repository_full_name"`
	IssueNumber              string `json:"issue_number"`
	OriginalCommentID        string `json:"original_comment_id"`
	AcknowledgementCommentID string `json:"acknowledgement_comment_id"`
	PolicyRevision           string `json:"policy_revision"`
	ReleaseLine              string `json:"release_line,omitempty"`
	Marker                   string `json:"marker"`
}

type OperationInspection struct {
	OK                       bool   `json:"ok"`
	SchemaVersion            string `json:"schema_version"`
	Phase                    string `json:"phase"`
	Command                  string `json:"command"`
	Version                  string `json:"requested_version"`
	RequestKey               string `json:"request_key"`
	RequestSHA256            string `json:"request_sha256"`
	RepositoryID             string `json:"repository_id"`
	RepositoryFullName       string `json:"repository_full_name"`
	IssueNumber              string `json:"issue_number"`
	OriginalCommentID        string `json:"original_comment_id"`
	AcknowledgementCommentID string `json:"acknowledgement_comment_id"`
	PolicyRevision           string `json:"policy_revision"`
}

func InspectRequest(inputRaw, policyRaw []byte) (RequestInspection, error) {
	request, err := DecodeDispatchJSON(inputRaw)
	if err != nil {
		return RequestInspection{}, err
	}
	policy, err := DecodePolicy(policyRaw)
	if err != nil {
		return RequestInspection{}, err
	}
	if request.Context.PolicyRevision != policy.Revision {
		return RequestInspection{}, typedContractError("policy_drift")
	}
	if request.Context.RepositoryID != policy.RepositoryID || request.Context.RepositoryFullName != policy.RepositoryFullName {
		return RequestInspection{}, typedRequestError("invalid_repository")
	}
	releaseLine, ok := policy.IssueMapping[request.Context.IssueNumber]
	if !ok {
		return RequestInspection{}, typedRequestError("unmapped_issue")
	}
	if request.Version != "auto" && releaseLineFromVersion(request.Version) != releaseLine {
		return RequestInspection{}, typedRequestError("line_mismatch")
	}
	return RequestInspection{
		OK:                       true,
		Command:                  request.Command,
		Version:                  request.Version,
		RequestKey:               request.RequestKey,
		RequestSHA256:            request.RequestSHA256,
		RepositoryID:             request.Context.RepositoryID,
		RepositoryFullName:       request.Context.RepositoryFullName,
		IssueNumber:              request.Context.IssueNumber,
		OriginalCommentID:        request.Context.OriginalCommentID,
		AcknowledgementCommentID: request.Context.AcknowledgementCommentID,
		PolicyRevision:           request.Context.PolicyRevision,
		ReleaseLine:              releaseLine,
		Marker:                   request.Marker,
	}, nil
}

func InspectOperation(raw []byte) (OperationInspection, error) {
	fields, err := decodeStrictObject(raw, MaxContextBytes, typedContractError("invalid_operation_json"), typedContractError("trailing_operation_json"), typedContractError("duplicate_operation_key"))
	if err != nil {
		if err == errStrictJSONNotObject {
			return OperationInspection{}, typedContractError("wrong_operation_type")
		}
		return OperationInspection{}, err
	}
	allowed := map[string]bool{
		"schema_version":             true,
		"phase":                      true,
		"request_key":                true,
		"request_sha256":             true,
		"repository_id":              true,
		"repository_full_name":       true,
		"issue_number":               true,
		"original_comment_id":        true,
		"acknowledgement_comment_id": true,
		"command":                    true,
		"requested_version":          true,
		"policy_revision":            true,
	}
	for key := range fields {
		if !allowed[key] {
			return OperationInspection{}, typedContractError("unknown_operation_key")
		}
	}
	read := func(key string) (string, error) {
		return stringField(fields, key, typedContractError("missing_operation_key"), typedContractError("wrong_operation_type"))
	}
	inspection := OperationInspection{OK: true}
	var errRead error
	if inspection.SchemaVersion, errRead = read("schema_version"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.SchemaVersion != PolicySchemaVersion {
		return OperationInspection{}, typedContractError("unsupported_operation_version")
	}
	if inspection.Phase, errRead = read("phase"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if !knownOperationPhase(inspection.Phase) {
		return OperationInspection{}, typedContractError("unknown_operation_phase")
	}
	if inspection.RequestKey, errRead = read("request_key"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.RequestSHA256, errRead = read("request_sha256"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.RepositoryID, errRead = read("repository_id"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.RepositoryFullName, errRead = read("repository_full_name"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.IssueNumber, errRead = read("issue_number"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.OriginalCommentID, errRead = read("original_comment_id"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.AcknowledgementCommentID, errRead = read("acknowledgement_comment_id"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.Command, errRead = read("command"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.Version, errRead = read("requested_version"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if inspection.PolicyRevision, errRead = read("policy_revision"); errRead != nil {
		return OperationInspection{}, errRead
	}
	if !validID(inspection.RepositoryID) || !validID(inspection.IssueNumber) || !validID(inspection.OriginalCommentID) || !validID(inspection.AcknowledgementCommentID) {
		return OperationInspection{}, typedRequestError("unsafe_id")
	}
	if inspection.RepositoryFullName != RepositoryFullName || inspection.RequestKey != RequestKey(inspection.RepositoryID, inspection.OriginalCommentID) {
		return OperationInspection{}, typedRequestError("source_mismatch")
	}
	if !isReleaseCommand(inspection.Command) {
		return OperationInspection{}, typedRequestError("unknown_release_command")
	}
	if inspection.Version != "auto" {
		normalized, _, err := NormalizeVersion(inspection.Command, inspection.Version)
		if err != nil {
			return OperationInspection{}, err
		}
		if normalized != inspection.Version {
			return OperationInspection{}, typedRequestError("invalid_version")
		}
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(inspection.RequestSHA256) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(inspection.PolicyRevision) {
		return OperationInspection{}, typedContractError("wrong_operation_type")
	}
	return inspection, nil
}

func knownOperationPhase(phase string) bool {
	switch phase {
	case "reserved", "signed", "branches_published", "tests_passed", "tags_published", "complete", "failed":
		return true
	default:
		return false
	}
}

func MarshalInspection(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
