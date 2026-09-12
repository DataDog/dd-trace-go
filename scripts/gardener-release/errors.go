// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "errors"

type ErrorClass string

const (
	ErrorClassRequestRejected    ErrorClass = "request_rejected"
	ErrorClassContractMismatch   ErrorClass = "contract_mismatch"
	ErrorClassEvidenceIncomplete ErrorClass = "evidence_incomplete"
	ErrorClassStateConflict      ErrorClass = "state_conflict"
	ErrorClassGenerationFailed   ErrorClass = "generation_failed"
	ErrorClassTestGateFailed     ErrorClass = "test_gate_failed"
	ErrorClassPublicationPartial ErrorClass = "publication_partial"
	ErrorClassImagesFailed       ErrorClass = "images_failed"
	ErrorClassFeedbackFailed     ErrorClass = "feedback_failed"
)

type ReleaseError struct {
	Class  ErrorClass
	Code   string
	Detail map[string]string
	Cause  error
}

func (e *ReleaseError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *ReleaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func ClassOf(err error) ErrorClass {
	var releaseErr *ReleaseError
	if errors.As(err, &releaseErr) {
		return releaseErr.Class
	}
	return classForCode(ErrorCode(err))
}

func NewCLIError(code string) *ReleaseError {
	return newReleaseError(ErrorClassContractMismatch, code)
}

func newReleaseError(class ErrorClass, code string) *ReleaseError {
	return &ReleaseError{Class: class, Code: code}
}

func wrapReleaseError(class ErrorClass, code string, cause error) *ReleaseError {
	return &ReleaseError{Class: class, Code: code, Cause: cause}
}

func classForCode(code string) ErrorClass {
	switch code {
	case "command_not_exact", "control_character", "extra_tokens", "invalid_repository", "invalid_version", "multiple_commands", "non_ascii", "prepare_patch_not_zero", "unknown_release_command", "unsafe_id", "version_overflow":
		return ErrorClassRequestRejected
	case "duplicate_context_key", "invalid_contract_version", "invalid_context_json", "trailing_context_json", "unknown_context_key", "unknown_input_key", "wrong_context_type":
		return ErrorClassContractMismatch
	default:
		if code == "" {
			return ""
		}
		return ErrorClassEvidenceIncomplete
	}
}
