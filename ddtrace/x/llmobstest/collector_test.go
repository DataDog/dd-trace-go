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

// TestLLMObsSpanAliasSurface pins the parts of the aliased span-event type that
// this package makes publicly reachable. LLMObsSpan is an alias for an internal
// wire struct, so a change there silently changes this package's exported
// surface — a test asserting a span-link ID as a number, say, would stop
// compiling for anyone using it. The assertions below are the surface contract;
// changing them is a deliberate break, not a refactor.
func TestLLMObsSpanAliasSurface(t *testing.T) {
	raw := []byte(`{
		"span_id": "222",
		"trace_id": "111",
		"span_links": [{"trace_id": "888", "span_id": "999", "trace_id_high": "1"}],
		"_dd": {"span_id": "222", "trace_id": "111", "apm_trace_id": "aabbccdd"}
	}`)

	var span LLMObsSpan
	require.NoError(t, json.Unmarshal(raw, &span))

	assert.Equal(t, "222", span.SpanID)
	assert.Equal(t, "111", span.TraceID)

	require.Len(t, span.SpanLinks, 1)
	// Span-link IDs are decimal strings on the wire, for both the live tracer and
	// the offline export path.
	assert.Equal(t, "888", span.SpanLinks[0].TraceID)
	assert.Equal(t, "999", span.SpanLinks[0].SpanID)
	assert.Equal(t, "1", span.SpanLinks[0].TraceIDHigh)

	assert.Equal(t, "aabbccdd", span.DDAttributes.APMTraceID)
}
