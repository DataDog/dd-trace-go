// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

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

	// Sent is the number of events delivered in requests that succeeded.
	Sent int
	// Dropped is the number of input rows dropped before sending (structural
	// validation failures and rows that could not be JSON-encoded); it equals
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

// ValidationError describes an input row that failed structural validation and
// was not sent.
type ValidationError struct {
	// Index is the zero-based position of the offending row in the input slice.
	Index int
	// Reason is a human-readable explanation of why the row was rejected.
	Reason string
}
