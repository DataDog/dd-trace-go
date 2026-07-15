// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

// ExportResult reports the outcome of an ExportSpans or ExportEvaluations call.
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

// ValidationError describes an input row that failed structural validation and
// was not sent.
type ValidationError struct {
	// Index is the zero-based position of the offending row in the input slice.
	Index int
	// Reason is a human-readable explanation of why the row was rejected.
	Reason string
}

// Failed returns the number of requests that did not succeed.
func (r *ExportResult) Failed() int {
	n := 0
	for _, req := range r.Requests {
		if req.Err != nil {
			n++
		}
	}
	return n
}

// OK reports whether every request succeeded. It does not consider validation
// errors, which represent caller-owned invalid input rather than a send failure.
func (r *ExportResult) OK() bool {
	return r.Failed() == 0
}
