// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpanLinkWireShape locks the dual span-link ID representation: the live
// tracer path emits numeric IDs (JSON numbers, its historical wire shape) while
// the offline export path emits opaque string IDs (JSON strings). TraceIDHigh is
// omitted unless set.
func TestSpanLinkWireShape(t *testing.T) {
	cases := []struct {
		name string
		link SpanLink
		want string
	}{
		{
			name: "numeric live path, no high word",
			link: SpanLink{TraceID: NumericSpanLinkID(111), SpanID: NumericSpanLinkID(222)},
			want: `{"trace_id":111,"span_id":222}`,
		},
		{
			name: "numeric live path with high word",
			link: func() SpanLink {
				h := NumericSpanLinkID(333)
				return SpanLink{TraceID: NumericSpanLinkID(111), TraceIDHigh: &h, SpanID: NumericSpanLinkID(222)}
			}(),
			want: `{"trace_id":111,"trace_id_high":333,"span_id":222}`,
		},
		{
			name: "opaque string export path",
			link: SpanLink{
				TraceID:    StringSpanLinkID("lt"),
				SpanID:     StringSpanLinkID("ls"),
				Attributes: map[string]string{"a": "b"},
			},
			want: `{"trace_id":"lt","span_id":"ls","attributes":{"a":"b"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.link)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(b))
		})
	}
}

// TestSpanLinkRoundTrip guards decoding: the in-process test collector (and any
// intake-response path) decodes transport.SpanLink, so SpanLinkID must accept
// both a JSON number and a JSON string. A marshal-only type silently breaks
// json.Unmarshal, dropping any span that carries links.
func TestSpanLinkRoundTrip(t *testing.T) {
	for _, wire := range []string{
		`{"trace_id":111,"trace_id_high":333,"span_id":222}`,
		`{"trace_id":"lt","span_id":"ls","attributes":{"a":"b"}}`,
	} {
		var link SpanLink
		require.NoError(t, json.Unmarshal([]byte(wire), &link))
		b, err := json.Marshal(link)
		require.NoError(t, err)
		assert.Equal(t, wire, string(b), "round-trip must preserve the wire shape")
	}
}
