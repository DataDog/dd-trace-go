// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import "fmt"

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
}

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

// ErrorCode classifies why a row was rejected, so callers can branch on the
// cause without matching on [ValidationError.Reason] text.
type ErrorCode string

// The reasons a row is dropped before sending.
const (
	// ErrMissingID: the span has no span_id or no trace_id.
	ErrMissingID ErrorCode = "missing_id"
	// ErrMissingKind: the span has no Kind.
	ErrMissingKind ErrorCode = "missing_kind"
	// ErrInvalidKind: the span's Kind is not one of the recognized kinds.
	ErrInvalidKind ErrorCode = "invalid_kind"
	// ErrInvalidStatus: the span's Status is neither StatusOK nor StatusError.
	ErrInvalidStatus ErrorCode = "invalid_status"
	// ErrMissingLabel: the evaluation metric has no Label.
	ErrMissingLabel ErrorCode = "missing_label"
	// ErrInvalidJoin: the evaluation metric does not specify exactly one complete
	// join family (span ID or tag).
	ErrInvalidJoin ErrorCode = "invalid_join"
	// ErrInvalidValue: the evaluation metric's value set is empty, ambiguous, or
	// not representable (e.g. a non-finite score, an empty json_value).
	ErrInvalidValue ErrorCode = "invalid_value"
	// ErrTypeMismatch: the evaluation metric's MetricType disagrees with the value
	// it carries, or names an unknown type.
	ErrTypeMismatch ErrorCode = "type_mismatch"
	// ErrNotEncodable: the row holds a value encoding/json cannot marshal.
	ErrNotEncodable ErrorCode = "not_encodable"
)

// ValidationError describes an input row that failed validation and was not sent.
// It implements error so a caller can return one directly.
type ValidationError struct {
	// Index is the zero-based position of the offending row in the input slice.
	Index int
	// Code classifies the rejection.
	Code ErrorCode
	// Reason is a human-readable explanation of why the row was rejected.
	Reason string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("llmobs/export: row %d rejected (%s): %s", e.Index, e.Code, e.Reason)
}
