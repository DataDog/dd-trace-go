// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobstest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLLMObsSpanAliasSurface pins the JSON contract of the span-event type this
// package re-exports. LLMObsSpan is an alias for an internal wire struct, so its
// json tags are part of this package's public behaviour even though the diff that
// changes them touches no file here: renaming a tag still compiles everywhere and
// would silently leave every downstream assertion reading a zero value.
//
// Field TYPE changes are not caught here — assert.Equal takes any, and the module
// stops building on its own because those types are load-bearing across
// internal/llmobs. This test's job is the tags and the decoded shape.
func TestLLMObsSpanAliasSurface(t *testing.T) {
	raw := []byte(`{
		"span_id": "222",
		"trace_id": "111",
		"parent_id": "undefined",
		"session_id": "sess",
		"tags": ["ml_app:app", "error:0"],
		"name": "chat",
		"service": "svc",
		"start_ns": 1000,
		"duration": 500,
		"status": "error",
		"status_message": "boom",
		"meta": {"span.kind": "llm", "input": {"value": "hi"}},
		"metrics": {"input_tokens": 12},
		"collection_errors": ["dropped_io"],
		"span_links": [{"trace_id": "888", "span_id": "999", "trace_id_high": "1", "attributes": {"a": "b"}}],
		"_dd": {"span_id": "222", "trace_id": "111", "apm_trace_id": "aabbccdd", "scope": "sc"}
	}`)

	var span LLMObsSpan
	require.NoError(t, json.Unmarshal(raw, &span))

	assert.Equal(t, "222", span.SpanID)
	assert.Equal(t, "111", span.TraceID)
	assert.Equal(t, "undefined", span.ParentID)
	assert.Equal(t, "sess", span.SessionID)
	assert.Equal(t, []string{"ml_app:app", "error:0"}, span.Tags)
	assert.Equal(t, "chat", span.Name)
	assert.Equal(t, "svc", span.Service)
	assert.Equal(t, int64(1000), span.StartNS)
	assert.Equal(t, int64(500), span.Duration)
	assert.Equal(t, "error", span.Status)
	assert.Equal(t, "boom", span.StatusMessage)
	assert.Equal(t, "llm", span.Meta["span.kind"])
	assert.Equal(t, map[string]any{"value": "hi"}, span.Meta["input"])
	assert.Equal(t, map[string]float64{"input_tokens": 12}, span.Metrics)
	assert.Equal(t, []string{"dropped_io"}, span.CollectionErrors)

	require.Len(t, span.SpanLinks, 1)
	// Span-link IDs are decimal strings on the wire, for both the live tracer and
	// the offline export path.
	assert.Equal(t, "888", span.SpanLinks[0].TraceID)
	assert.Equal(t, "999", span.SpanLinks[0].SpanID)
	assert.Equal(t, "1", span.SpanLinks[0].TraceIDHigh)
	assert.Equal(t, map[string]string{"a": "b"}, span.SpanLinks[0].Attributes)

	assert.Equal(t, "222", span.DDAttributes.SpanID)
	assert.Equal(t, "111", span.DDAttributes.TraceID)
	assert.Equal(t, "aabbccdd", span.DDAttributes.APMTraceID)
	assert.Equal(t, "sc", span.DDAttributes.Scope)
}
