// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

// ExportResult reports the outcome of an ExportTraces/ExportMetrics/ExportLogs
// call. Each input request maps to exactly one RequestResult, by index.
type ExportResult struct {
	Requests []RequestResult
}

// RequestResult reports the outcome of a single request's POST.
type RequestResult struct {
	// Index is the position of this request in the input slice.
	Index int
	// StatusCode is the final HTTP status code (0 if no response was received).
	StatusCode int
	// Attempts is the number of HTTP attempts made, including retries.
	Attempts int
	// Retriable reports whether the failure class was transient. It is only
	// meaningful when Err is non-nil.
	Retriable bool
	// ResponseSnippet is a bounded, UTF-8-safe excerpt of the response body.
	ResponseSnippet string
	// Err is the error for this request, or nil on success.
	Err error
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

// OK reports whether every request succeeded.
func (r *ExportResult) OK() bool {
	return r.Failed() == 0
}
