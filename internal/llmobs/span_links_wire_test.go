// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToTransportSpanLinksWire locks the live-tracer span-link wire shape produced
// by toTransportSpanLinks: numeric IDs marshal as JSON numbers (the tracer's
// historical wire), and a zero TraceIDHigh is omitted rather than serialized as 0.
// The dual-representation SpanLinkID type is tested in isolation in the transport
// package; this guards the conversion the live path actually runs, so a refactor
// that wrapped IDs as strings or dropped the TraceIDHigh guard cannot silently
// flip the wire (from `"trace_id":123` to `"trace_id":"123"`, or emit
// `trace_id_high:0`) with every test still green.
func TestToTransportSpanLinksWire(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		assert.Nil(t, toTransportSpanLinks(nil))
		assert.Nil(t, toTransportSpanLinks([]SpanLink{}))
	})

	cases := []struct {
		name string
		in   SpanLink
		want string
	}{
		{
			name: "numeric, zero high word omitted",
			in:   SpanLink{TraceID: 111, SpanID: 222},
			want: `{"trace_id":111,"span_id":222}`,
		},
		{
			name: "numeric with high word",
			in:   SpanLink{TraceID: 111, SpanID: 222, TraceIDHigh: 333},
			want: `{"trace_id":111,"trace_id_high":333,"span_id":222}`,
		},
		{
			name: "attributes preserved",
			in:   SpanLink{TraceID: 111, SpanID: 222, Attributes: map[string]string{"a": "b"}},
			want: `{"trace_id":111,"span_id":222,"attributes":{"a":"b"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toTransportSpanLinks([]SpanLink{tc.in})
			require.Len(t, got, 1)
			b, err := json.Marshal(got[0])
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(b))
		})
	}
}

// TestSpanLinkJSONTags guards the JSON wire shape of SpanLink itself (the public
// llmobs.SpanLink aliases this type). Callers that persist/replay links via
// encoding/json must keep getting snake_case trace_id/span_id (the shape from
// before SpanLink was split out from transport.SpanLink), not the Go field names.
func TestSpanLinkJSONTags(t *testing.T) {
	b, err := json.Marshal(SpanLink{TraceID: 111, SpanID: 222})
	require.NoError(t, err)
	assert.JSONEq(t, `{"trace_id":111,"span_id":222}`, string(b))

	b, err = json.Marshal(SpanLink{
		TraceID: 111, TraceIDHigh: 333, SpanID: 222,
		Attributes: map[string]string{"a": "b"}, Tracestate: "ts", Flags: 1,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"trace_id":111,"trace_id_high":333,"span_id":222,"attributes":{"a":"b"},"tracestate":"ts","flags":1}`, string(b))
}
