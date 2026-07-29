// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"fmt"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// ExportResult reports a submission outcome.
type ExportResult struct {
	// Requests contains one result per HTTP request.
	Requests []RequestResult
	// ValidationErrors contains rows rejected before sending.
	ValidationErrors []ValidationError

	// Sent, Dropped, and Failed partition the input rows.
	Sent    int
	Dropped int
	Failed  int

	cancelErr error
}

func (r *ExportResult) recordCancel(remaining int, err error) error {
	if remaining > 0 {
		r.Requests = append(r.Requests, RequestResult{
			Index: len(r.Requests),
			Count: remaining,
			Err:   fmt.Errorf("llmobs/export: batch not sent, export canceled: %w", err),
		})
	}
	if r.cancelErr == nil {
		r.cancelErr = fmt.Errorf("llmobs/export: export canceled: %w", err)
	}
	r.finalize()
	return r.cancelErr
}

func (r *ExportResult) canceledErr() error { return r.cancelErr }

func (r *ExportResult) finalize() int {
	r.Sent, r.Failed = 0, 0
	failedReqs := 0
	for _, req := range r.Requests {
		if req.Err != nil {
			failedReqs++
			r.Failed += req.Count
			continue
		}
		r.Sent += req.Count
	}
	r.Dropped = len(r.ValidationErrors)
	return failedReqs
}

// RequestResult reports one HTTP request.
type RequestResult struct {
	Index           int
	Count           int
	StatusCode      int
	Attempts        int
	Retriable       bool
	ResponseSnippet string
	Err             error
}

// ErrorCode classifies why a row was rejected.
type ErrorCode = illmobs.ExportValidationCode

const (
	CodeMissingID     ErrorCode = illmobs.ExportCodeMissingID
	CodeMissingKind   ErrorCode = illmobs.ExportCodeMissingKind
	CodeInvalidKind   ErrorCode = illmobs.ExportCodeInvalidKind
	CodeInvalidStatus ErrorCode = illmobs.ExportCodeInvalidStatus
	CodeMissingLabel  ErrorCode = illmobs.ExportCodeMissingLabel
	CodeInvalidJoin   ErrorCode = illmobs.ExportCodeInvalidJoin
	CodeInvalidValue  ErrorCode = illmobs.ExportCodeInvalidValue
	CodeTypeMismatch  ErrorCode = illmobs.ExportCodeTypeMismatch
	CodeNotEncodable  ErrorCode = illmobs.ExportCodeNotEncodable
)

// ValidationError describes an input row that was not sent.
type ValidationError = illmobs.ExportValidationError
