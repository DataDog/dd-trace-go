// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awslambda

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// span is a finished span, as the tracer writes it to stdout in Lambda mode
// (logTraceWriter in ddtrace/tracer/writer.go). IDs are hexadecimal.
type span struct {
	TraceID  string            `json:"trace_id"`
	SpanID   string            `json:"span_id"`
	ParentID string            `json:"parent_id"`
	Error    int               `json:"error"`
	Name     string            `json:"name"`
	Resource string            `json:"resource"`
	Start    int64             `json:"start"`
	Duration int64             `json:"duration"`
	Meta     map[string]string `json:"meta"`
}

// parseSpans decodes every span written to stdout. Other output lines, such as
// ddlambda's own logs, are skipped.
func parseSpans(t *testing.T, stdout []byte) []span {
	t.Helper()
	var spans []span
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	// The tracer writes lines of up to 256KiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, `{"traces"`) {
			continue
		}
		var payload struct {
			Traces [][]span `json:"traces"`
		}
		require.NoErrorf(t, json.Unmarshal([]byte(line), &payload), "decoding %s", line)
		for _, trace := range payload.Traces {
			spans = append(spans, trace...)
		}
	}
	require.NoError(t, scanner.Err())
	return spans
}
