// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestWithTraceContext(t *testing.T) {
	traceContext := []byte(`{"x-datadog-trace-id":"1","x-datadog-parent-id":"2"}`)
	want := map[string]any{"x-datadog-trace-id": "1", "x-datadog-parent-id": "2"}

	tests := []struct {
		name     string
		metadata string
		wantRest map[string]any
		wantErr  bool
	}{
		{name: "nil", metadata: ""},
		{name: "empty object", metadata: `{}`},
		{name: "empty object with spaces", metadata: " { } \n"},
		{name: "null", metadata: `null`},
		{name: "other keys", metadata: `{"customer":"acme","count":3}`, wantRest: map[string]any{"customer": "acme", "count": 3.0}},
		{name: "replaces a stale trace context", metadata: `{"_datadog":{"x-datadog-trace-id":"9"},"a":true}`, wantRest: map[string]any{"a": true}},
		{name: "replaces an escaped stale trace context", metadata: `{"\u005fdatadog":{"x-datadog-trace-id":"9"},"a":"say \"hi\""}`, wantRest: map[string]any{"a": `say "hi"`}},
		{name: "spaces around members", metadata: " {\n  \"a\" : [1, {\"b\": null}] \n} ", wantRest: map[string]any{"a": []any{1.0, map[string]any{"b": nil}}}},
		{name: "array", metadata: `[1]`, wantErr: true},
		{name: "invalid member", metadata: `{"a":}`, wantErr: true},
		{name: "unquoted key", metadata: `{a:1}`, wantErr: true},
		{name: "string", metadata: `"text"`, wantErr: true},
		{name: "invalid", metadata: `{"a":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var metadata []byte
			if tt.metadata != "" {
				metadata = []byte(tt.metadata)
			}
			got, err := withTraceContext(metadata, traceContext)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(got, &decoded), "%s", got)
			assert.Equal(t, want, decoded[metadataKey])
			delete(decoded, metadataKey)
			if tt.wantRest == nil {
				tt.wantRest = map[string]any{}
			}
			assert.Equal(t, tt.wantRest, decoded)
		})
	}
}

func TestExtractFromMetadata(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tests := []struct {
		name     string
		metadata string
		wantNil  bool
	}{
		{name: "nil", metadata: "", wantNil: true},
		{name: "no trace context", metadata: `{"a":1}`, wantNil: true},
		{name: "key only in a value", metadata: `{"note":"\"_datadog\""}`, wantNil: true},
		{name: "not strings", metadata: `{"_datadog":{"x-datadog-trace-id":1}}`, wantNil: true},
		{name: "no ids", metadata: `{"_datadog":{"foo":"bar"}}`, wantNil: true},
		{name: "valid", metadata: `{"a":1,"_datadog":{"x-datadog-trace-id":"10","x-datadog-parent-id":"20","x-datadog-sampling-priority":"2"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sctx := extractFromMetadata([]byte(tt.metadata))
			if tt.wantNil {
				assert.Nil(t, sctx)
				return
			}
			require.NotNil(t, sctx)
			assert.Equal(t, uint64(20), sctx.SpanID())
			assert.Equal(t, uint64(10), sctx.TraceIDLower())
			priority, ok := sctx.SamplingPriority()
			assert.True(t, ok)
			assert.Equal(t, 2, priority)
		})
	}
}
