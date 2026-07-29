// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"fmt"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// ExportResult reports the outcome of a SubmitSpans or SubmitEvaluations call.
//
// A call may produce multiple HTTP requests (one per chunk); each is reported in
// Requests. Rows that fail structural validation are never sent and are reported
// in ValidationErrors instead, so callers can distinguish invalid input from
// transport failures while retaining their own dedup/outbox behavior.
type ExportResult struct {
	// Requests holds one entry per HTTP request performed, in order.
	Requests []RequestResult
	// ValidationErrors holds input rows dropped before sending, by input index.
	ValidationErrors []ValidationError

	// Sent, Dropped and Failed partition the input: every input row is counted in
	// exactly one of them, so Sent+Dropped+Failed always equals len(input) — a
	// canceled call included, where the rows it never validated or sent are
	// reported as Failed through a final not-sent entry in Requests.

	// Sent is the number of events delivered in requests that succeeded.
	Sent int
	// Dropped is the number of input rows dropped before sending (validation
	// failures and rows that could not be JSON-encoded); it equals
	// len(ValidationErrors).
	Dropped int
	// Failed is the number of events in requests that failed to send.
	Failed int

	// cancelErr is set only when a cancellation actually abandoned rows. A cancel
	// that lands after the last row was delivered leaves it nil, so the call still
	// reports success.
	cancelErr error
}

// recordCancel accounts the rows a canceled call never validated or sent as one
// not-sent request, keeping the Sent/Dropped/Failed invariant above, and returns
// the error the call reports.
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

// canceledErr reports the cancellation error when rows were abandoned, else nil.
func (r *ExportResult) canceledErr() error { return r.cancelErr }

// finalize populates Sent, Dropped and Failed from the accumulated per-request
// outcomes and validation errors, and returns the number of failed requests
// (what exportutil.Aggregate summarizes). It resets the counters first so it is
// safe to call once per return path.
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

// RequestResult reports the outcome of a single HTTP request (one chunk).
type RequestResult struct {
	// Index is the zero-based position of this chunk within the call.
	Index int
	// Count is the number of events in this chunk.
	Count int
	// StatusCode is the final HTTP status code (0 if no response was received).
	StatusCode int
	// Attempts is the number of HTTP attempts made, including retries.
	Attempts int
	// Retriable reports whether the failure class was transient (safe to retry).
	// It is only meaningful when Err is non-nil.
	Retriable bool
	// ResponseSnippet is a bounded excerpt of the response body, if any.
	ResponseSnippet string
	// Err is the transport error for this chunk, or nil on success.
	Err error
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
