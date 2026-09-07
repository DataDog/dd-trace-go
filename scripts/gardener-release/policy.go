// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

const (
	PolicySchemaVersion         = "1"
	StateBranch                 = "gardener-release-state"
	ReleaseConcurrencyGroup     = "gardener-release-production-v1"
	MaxPolicyBytes              = 64 * 1024
	MaxGitHubResponseBytes      = 16 * 1024 * 1024
	MaxGitHubPages              = 200
	GitHubPageSize              = 100
	MaxReadRetries              = 3
	MaxIssueMappings            = 1024
	MaxPollingDeadlineSeconds   = 6 * 60 * 60
	DefaultPollingDeadlineSecs  = 30 * 60
	ProductionGitHubAPIEndpoint = "https://api.github.com"
)

type Policy struct {
	SchemaVersion           string
	RepositoryID            string
	RepositoryFullName      string
	StateBranch             string
	ReleaseConcurrencyGroup string
	IssueMapping            map[string]string
	Limits                  PolicyLimits
	Revision                string
}

type PolicyLimits struct {
	APIMaxPages            int
	APIPageSize            int
	APIResponseBytes       int64
	ReadRetries            int
	PollingDeadlineSeconds int
}

type OriginalComment struct {
	RepositoryID       string
	RepositoryFullName string
	IssueNumber        string
	CommentID          string
	Body               string
	AuthorAssociation  string
}

type ValidatedRequest struct {
	RequestKey               string `json:"request_key"`
	RequestSHA256            string `json:"request_sha256"`
	Command                  string `json:"command"`
	Version                  string `json:"version"`
	ReleaseLine              string `json:"release_line"`
	RepositoryID             string `json:"repository_id"`
	RepositoryFullName       string `json:"repository_full_name"`
	IssueNumber              string `json:"issue_number"`
	OriginalCommentID        string `json:"original_comment_id"`
	AcknowledgementCommentID string `json:"acknowledgement_comment_id"`
	PolicyRevision           string `json:"policy_revision"`
	Marker                   string `json:"marker"`
}

func DecodePolicy(raw []byte) (Policy, error) {
	fields, err := decodeStrictObject(raw, MaxPolicyBytes, typedContractError("invalid_policy_json"), typedContractError("trailing_policy_json"), typedContractError("duplicate_policy_key"))
	if err != nil {
		if err == errStrictJSONNotObject {
			return Policy{}, typedContractError("wrong_policy_type")
		}
		return Policy{}, err
	}
	allowed := map[string]bool{
		"schema_version":            true,
		"repository_id":             true,
		"repository_full_name":      true,
		"state_branch":              true,
		"release_concurrency_group": true,
		"issue_mapping":             true,
		"limits":                    true,
	}
	for key := range fields {
		if !allowed[key] {
			return Policy{}, typedContractError("unknown_policy_key")
		}
	}
	policy := Policy{}
	if err := stringPolicyField(fields, "schema_version", &policy.SchemaVersion); err != nil {
		return Policy{}, err
	}
	if policy.SchemaVersion != PolicySchemaVersion {
		return Policy{}, typedContractError("unsupported_policy_version")
	}
	if err := stringPolicyField(fields, "repository_id", &policy.RepositoryID); err != nil {
		return Policy{}, err
	}
	if !validID(policy.RepositoryID) {
		return Policy{}, typedRequestError("unsafe_id")
	}
	if err := stringPolicyField(fields, "repository_full_name", &policy.RepositoryFullName); err != nil {
		return Policy{}, err
	}
	if policy.RepositoryFullName != RepositoryFullName {
		return Policy{}, typedRequestError("invalid_repository")
	}
	if err := stringPolicyField(fields, "state_branch", &policy.StateBranch); err != nil {
		return Policy{}, err
	}
	if policy.StateBranch != StateBranch {
		return Policy{}, typedContractError("policy_drift")
	}
	if err := stringPolicyField(fields, "release_concurrency_group", &policy.ReleaseConcurrencyGroup); err != nil {
		return Policy{}, err
	}
	if policy.ReleaseConcurrencyGroup != ReleaseConcurrencyGroup {
		return Policy{}, typedContractError("policy_drift")
	}
	policy.IssueMapping, err = decodeIssueMapping(fields["issue_mapping"])
	if err != nil {
		return Policy{}, err
	}
	policy.Limits, err = decodePolicyLimits(fields["limits"])
	if err != nil {
		return Policy{}, err
	}
	policy.Revision = PolicyRevision(raw)
	return policy, nil
}

func PolicyRevision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ValidateRequestAgainstPolicy(request DispatchRequest, policy Policy, source OriginalComment) (ValidatedRequest, error) {
	if request.ContractVersion != ContractVersion {
		return ValidatedRequest{}, typedContractError("invalid_contract_version")
	}
	if request.Context.PolicyRevision != policy.Revision {
		return ValidatedRequest{}, typedContractError("policy_drift")
	}
	if request.Context.RepositoryID != policy.RepositoryID || request.Context.RepositoryFullName != policy.RepositoryFullName {
		return ValidatedRequest{}, typedRequestError("invalid_repository")
	}
	if source.RepositoryID != request.Context.RepositoryID || source.RepositoryFullName != request.Context.RepositoryFullName || source.IssueNumber != request.Context.IssueNumber || source.CommentID != request.Context.OriginalCommentID {
		return ValidatedRequest{}, typedRequestError("source_mismatch")
	}
	if source.Body != request.Context.BodySnapshot {
		return ValidatedRequest{}, typedRequestError("source_mismatch")
	}
	if !authorizedAssociation(source.AuthorAssociation) {
		return ValidatedRequest{}, typedRequestError("unauthorized_actor")
	}
	releaseLine, ok := policy.IssueMapping[request.Context.IssueNumber]
	if !ok {
		return ValidatedRequest{}, typedRequestError("unmapped_issue")
	}
	if request.Version != "auto" && releaseLineFromVersion(request.Version) != releaseLine {
		return ValidatedRequest{}, typedRequestError("line_mismatch")
	}
	return ValidatedRequest{
		RequestKey:               request.RequestKey,
		RequestSHA256:            request.RequestSHA256,
		Command:                  request.Command,
		Version:                  request.Version,
		ReleaseLine:              releaseLine,
		RepositoryID:             request.Context.RepositoryID,
		RepositoryFullName:       request.Context.RepositoryFullName,
		IssueNumber:              request.Context.IssueNumber,
		OriginalCommentID:        request.Context.OriginalCommentID,
		AcknowledgementCommentID: request.Context.AcknowledgementCommentID,
		PolicyRevision:           request.Context.PolicyRevision,
		Marker:                   request.Marker,
	}, nil
}

func stringPolicyField(fields map[string]json.RawMessage, key string, target *string) error {
	value, err := stringField(fields, key, typedContractError("missing_policy_key"), typedContractError("wrong_policy_type"))
	if err != nil {
		return err
	}
	*target = value
	return nil
}

func decodeIssueMapping(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, typedContractError("missing_policy_key")
	}
	fields, err := decodeStrictObject(raw, MaxPolicyBytes, typedContractError("invalid_policy_json"), typedContractError("trailing_policy_json"), typedContractError("duplicate_policy_key"))
	if err != nil {
		if err == errStrictJSONNotObject {
			return nil, typedContractError("wrong_policy_type")
		}
		return nil, err
	}
	if len(fields) == 0 || len(fields) > MaxIssueMappings {
		return nil, typedContractError("policy_limit_exceeded")
	}
	mapping := make(map[string]string, len(fields))
	for issue, rawLine := range fields {
		if !validID(issue) {
			return nil, typedRequestError("unsafe_id")
		}
		line, err := rawString(rawLine, typedContractError("wrong_policy_type"))
		if err != nil {
			return nil, err
		}
		if !validReleaseLine(line) {
			return nil, typedContractError("policy_drift")
		}
		mapping[issue] = line
	}
	return mapping, nil
}

func decodePolicyLimits(raw json.RawMessage) (PolicyLimits, error) {
	if len(raw) == 0 {
		return PolicyLimits{}, typedContractError("missing_policy_key")
	}
	fields, err := decodeStrictObject(raw, 4096, typedContractError("invalid_policy_json"), typedContractError("trailing_policy_json"), typedContractError("duplicate_policy_key"))
	if err != nil {
		if err == errStrictJSONNotObject {
			return PolicyLimits{}, typedContractError("wrong_policy_type")
		}
		return PolicyLimits{}, err
	}
	allowed := map[string]bool{
		"api_max_pages":            true,
		"api_page_size":            true,
		"api_response_bytes":       true,
		"read_retries":             true,
		"polling_deadline_seconds": true,
	}
	for key := range fields {
		if !allowed[key] {
			return PolicyLimits{}, typedContractError("unknown_policy_key")
		}
	}
	limits := PolicyLimits{}
	var errInt error
	if limits.APIMaxPages, errInt = intPolicyField(fields, "api_max_pages"); errInt != nil {
		return PolicyLimits{}, errInt
	}
	if limits.APIPageSize, errInt = intPolicyField(fields, "api_page_size"); errInt != nil {
		return PolicyLimits{}, errInt
	}
	if limits.APIResponseBytes, errInt = int64PolicyField(fields, "api_response_bytes"); errInt != nil {
		return PolicyLimits{}, errInt
	}
	if limits.ReadRetries, errInt = intPolicyField(fields, "read_retries"); errInt != nil {
		return PolicyLimits{}, errInt
	}
	if limits.PollingDeadlineSeconds, errInt = intPolicyField(fields, "polling_deadline_seconds"); errInt != nil {
		return PolicyLimits{}, errInt
	}
	if limits.APIMaxPages <= 0 || limits.APIMaxPages > MaxGitHubPages || limits.APIPageSize <= 0 || limits.APIPageSize > GitHubPageSize || limits.APIResponseBytes <= 0 || limits.APIResponseBytes > MaxGitHubResponseBytes || limits.ReadRetries < 0 || limits.ReadRetries > MaxReadRetries || limits.PollingDeadlineSeconds <= 0 || limits.PollingDeadlineSeconds > MaxPollingDeadlineSeconds {
		return PolicyLimits{}, typedContractError("policy_limit_exceeded")
	}
	return limits, nil
}

func rawString(raw json.RawMessage, wrongTypeErr error) (string, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", wrongTypeErr
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", wrongTypeErr
	}
	return value, nil
}

func intPolicyField(fields map[string]json.RawMessage, key string) (int, error) {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return 0, typedContractError("wrong_policy_type")
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, typedContractError("wrong_policy_type")
	}
	return value, nil
}

func int64PolicyField(fields map[string]json.RawMessage, key string) (int64, error) {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return 0, typedContractError("wrong_policy_type")
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, typedContractError("wrong_policy_type")
	}
	return value, nil
}

func authorizedAssociation(association string) bool {
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
}

func validReleaseLine(line string) bool {
	return regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(line)
}

func releaseLineFromVersion(version string) string {
	match := regexp.MustCompile(`^(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))\.(?:0|[1-9][0-9]*)$`).FindStringSubmatch(version)
	if match == nil {
		return ""
	}
	return match[1]
}

func typedContractError(code string) *ReleaseError {
	return newReleaseError(ErrorClassContractMismatch, code)
}

func typedRequestError(code string) *ReleaseError {
	return newReleaseError(ErrorClassRequestRejected, code)
}
