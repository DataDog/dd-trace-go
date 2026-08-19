// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const responseSnippetMaxBytes = 512

// Result reports a submission outcome.
type Result struct {
	// Requests contains one result for each input request, in input order.
	Requests []RequestResult
	// Sent and Failed partition the input requests.
	Sent   int
	Failed int
}

func (r *Result) finalize() {
	r.Sent, r.Failed = 0, 0
	for _, request := range r.Requests {
		if request.Err != nil {
			r.Failed++
			continue
		}
		r.Sent++
	}
}

// RequestResult reports one input request's outcome.
type RequestResult struct {
	// Index identifies the request in the input slice.
	Index int
	// StatusCode is the final HTTP status, or zero when no response was received.
	StatusCode int
	// Attempts includes the initial request and any retries.
	Attempts int
	// Retriable reports whether the failure is transient.
	Retriable bool
	// RejectedItems is the partial-success count reported by the OTLP endpoint.
	RejectedItems int64
	// ResponseSnippet is a bounded diagnostic excerpt of the response body.
	ResponseSnippet string
	// Err is nil only when the entire input request was accepted.
	Err error
}

func responseSnippet(body []byte) string {
	snippet := strings.ToValidUTF8(strings.TrimSpace(string(body)), "")
	if len(snippet) <= responseSnippetMaxBytes {
		return snippet
	}
	cut := responseSnippetMaxBytes
	for cut > 0 && !utf8.RuneStart(snippet[cut]) {
		cut--
	}
	return snippet[:cut]
}

func aggregateFailures(result *Result) error {
	requestErrors := make([]error, 0, result.Failed)
	for _, request := range result.Requests {
		if request.Err != nil {
			requestErrors = append(requestErrors, request.Err)
		}
	}
	if len(requestErrors) == 0 {
		return nil
	}
	return fmt.Errorf("otlp/export: %d of %d request(s) failed: %w", result.Failed, len(result.Requests), errors.Join(requestErrors...))
}
