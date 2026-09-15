// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"errors"
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/internal/exporttransport"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// Result reports a submission outcome.
type Result struct {
	// Requests contains one result per sent or abandoned batch.
	Requests []RequestResult
	// ValidationErrors contains rows rejected before sending.
	ValidationErrors []ValidationError

	// Sent, Dropped, and Failed partition the input rows.
	Sent    int
	Dropped int
	Failed  int

	cancelErr error
}

func (r *Result) recordCancel(indices []int, err error) error {
	if len(indices) > 0 {
		r.Requests = append(r.Requests, RequestResult{
			InputIndices: indices,
			Retriable:    true,
			Err:          fmt.Errorf("llmobs/export: batch not sent, export canceled: %w", err),
		})
	}
	if r.cancelErr == nil {
		r.cancelErr = fmt.Errorf("llmobs/export: export canceled: %w", err)
	}
	r.finalize()
	return r.cancelErr
}

func (r *Result) canceledErr() error { return r.cancelErr }

func (r *Result) finalize() {
	r.Sent, r.Failed = 0, 0
	for _, req := range r.Requests {
		if req.Err != nil {
			r.Failed += len(req.InputIndices)
			continue
		}
		r.Sent += len(req.InputIndices)
	}
	r.Dropped = len(r.ValidationErrors)
}

// RequestResult reports one sent or abandoned batch.
type RequestResult struct {
	// InputIndices identifies the input rows represented by this result.
	InputIndices    []int
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
	CodeInvalidTiming ErrorCode = illmobs.ExportCodeInvalidTiming
	CodeInvalidLink   ErrorCode = illmobs.ExportCodeInvalidLink
	CodeMissingLabel  ErrorCode = illmobs.ExportCodeMissingLabel
	CodeInvalidJoin   ErrorCode = illmobs.ExportCodeInvalidJoin
	CodeInvalidValue  ErrorCode = illmobs.ExportCodeInvalidValue
	CodeTypeMismatch  ErrorCode = illmobs.ExportCodeTypeMismatch
	CodeNotEncodable  ErrorCode = illmobs.ExportCodeNotEncodable
	CodeTooLarge      ErrorCode = illmobs.ExportCodeTooLarge
)

// ValidationError describes an input row that was not sent.
type ValidationError = illmobs.ExportValidationError

func applyResult(rr *RequestResult, result transport.RequestResult, err error) {
	rr.StatusCode = result.StatusCode
	rr.Attempts = result.Attempts
	rr.Retriable = result.Retriable
	rr.ResponseSnippet = exporttransport.ResponseSnippet(result.Body)
	rr.Err = err
}

func aggregateFailures(result *Result) error {
	var requestErrors []error
	for _, request := range result.Requests {
		if request.Err != nil {
			requestErrors = append(requestErrors, request.Err)
		}
	}
	if len(requestErrors) == 0 {
		return nil
	}
	return fmt.Errorf(
		"llmobs/export: %d of %d batch(es) failed: %w",
		len(requestErrors),
		len(result.Requests),
		errors.Join(requestErrors...),
	)
}

func inputIndices(start, end int) []int {
	indices := make([]int, end-start)
	for i := range indices {
		indices[i] = start + i
	}
	return indices
}
