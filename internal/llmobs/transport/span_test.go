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

func TestSpanLinkWireShape(t *testing.T) {
	cases := []struct {
		name string
		link SpanLink
		want string
	}{
		{
			name: "no high word",
			link: SpanLink{TraceID: "111", SpanID: "222"},
			want: `{"trace_id":"111","span_id":"222"}`,
		},
		{
			name: "with high word",
			link: SpanLink{TraceID: "111", TraceIDHigh: "333", SpanID: "222"},
			want: `{"trace_id":"111","trace_id_high":"333","span_id":"222"}`,
		},
		{
			name: "opaque IDs with attributes",
			link: SpanLink{
				TraceID:    "lt",
				SpanID:     "ls",
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

func TestSpanLinkRoundTrip(t *testing.T) {
	for _, wire := range []string{
		`{"trace_id":"111","trace_id_high":"333","span_id":"222"}`,
		`{"trace_id":"lt","span_id":"ls","attributes":{"a":"b"}}`,
	} {
		var link SpanLink
		require.NoError(t, json.Unmarshal([]byte(wire), &link))
		b, err := json.Marshal(link)
		require.NoError(t, err)
		assert.Equal(t, wire, string(b), "round-trip must preserve the wire shape")
	}
}
