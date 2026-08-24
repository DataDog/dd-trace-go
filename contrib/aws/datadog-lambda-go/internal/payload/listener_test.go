// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package payload

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testcases = []struct {
	name    string
	enabled bool
	msg     string
	expect  string
}{
	// disabled: always no-op
	{"disabled", false, `{"detail":{"_datadog":{"x":"y"},"foo":"bar"}}`, `{"detail":{"_datadog":{"x":"y"},"foo":"bar"}}`},

	// no-op: no _datadog present
	{"no datadog key", true, `{"detail":{"foo":"bar"}}`, `{"detail":{"foo":"bar"}}`},
	{"empty object", true, `{}`, `{}`},
	{"empty input", true, ``, ``},
	{"invalid JSON", true, `not even json`, `not even json`},
	{"SQS event", true, `{"Records":[{"body":"hello"}]}`, `{"Records":[{"body":"hello"}]}`},

	// object-form _datadog removal: byte splice preserves surrounding bytes exactly
	{"object detail - alone", true, `{"detail":{"_datadog":{"x":"y"}}}`, `{"detail":{}}`},
	{"object detail - at beginning", true, `{"detail":{"_datadog":{"x":"y"},"foo":"bar"}}`, `{"detail":{"foo":"bar"}}`},
	{"object detail - at end", true, `{"detail":{"foo":"bar","_datadog":{"x":"y"}}}`, `{"detail":{"foo":"bar"}}`},
	{"object detail - in middle", true, `{"detail":{"foo":"bar","_datadog":{"x":"y"},"baz":"qux"}}`, `{"detail":{"foo":"bar","baz":"qux"}}`},

	// regression: adjacent carriers caused overlap-range panic before notBefore fix
	{"regression: adjacent carriers", true, `{"_datadog":{},"_datadog":{}}`, `{}`},

	// regression: carrier value contains literal , and } characters
	{"regression: value with ,}", true, `{"foo":"a,}b","_datadog":{"x":1}}`, `{"foo":"a,}b"}`},
}

func strip(enabled bool, in json.RawMessage) json.RawMessage {
	l := MakeListener(Config{StripInjectedContext: enabled})
	_, out := l.HandlerStarted(context.Background(), in)
	return out
}

func TestStripInjectedContext(t *testing.T) {
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			out := strip(tc.enabled, json.RawMessage(tc.msg))
			assert.Equal(t, tc.expect, string(out))
		})
	}
}

// TestStripStringDetail covers the string-encoded detail path separately.
// json.Marshal does not guarantee whitespace or key order, so the output
// is verified semantically rather than by exact string comparison.
func TestStripStringDetail(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		wantKey string
		wantVal string
	}{
		{
			name:    "string detail: _datadog in middle",
			msg:     `{"detail":"{\"foo\":\"bar\",\"_datadog\":{\"k\":\"v\"}}"}`,
			wantKey: "foo",
			wantVal: "bar",
		},
		{
			name:    "regression: Bug E string detail - value contains }",
			msg:     `{"detail":"{\"foo\":\"bar\",\"_datadog\":{\"k\":\"v}z\"}}"}`,
			wantKey: "foo",
			wantVal: "bar",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := strip(true, json.RawMessage(tc.msg))

			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out, &envelope))

			var detailStr string
			require.NoError(t, json.Unmarshal(envelope["detail"], &detailStr))

			var detail map[string]string
			require.NoError(t, json.Unmarshal([]byte(detailStr), &detail))

			assert.NotContains(t, detail, datadogCarrierKey)
			assert.Equal(t, tc.wantVal, detail[tc.wantKey])
		})
	}
}

// TestStripInjectedContextFixtures verifies stripping against real-world EventBridge payloads.
func TestStripInjectedContextFixtures(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		fixture string
		verify  func(t *testing.T, in, out json.RawMessage)
	}{
		{
			name:    "strips full EventBridge object detail",
			enabled: true,
			fixture: "eventbridge-with-datadog-object.json",
			verify: func(t *testing.T, in, out json.RawMessage) {
				var env map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out, &env))
				assert.Equal(t, `"trace-propagation-test"`, string(env["detail-type"]))
				assert.Equal(t, `"trace-propagation.client"`, string(env["source"]))
				assert.Equal(t, `"test-event-id"`, string(env["id"]))
				var detail map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(env["detail"], &detail))
				assert.NotContains(t, detail, datadogCarrierKey)
				var foo string
				require.NoError(t, json.Unmarshal(detail["foo"], &foo))
				assert.Equal(t, "bar", foo)
			},
		},
		{
			name:    "strips full EventBridge string detail",
			enabled: true,
			fixture: "eventbridge-with-datadog-string-detail.json",
			verify: func(t *testing.T, in, out json.RawMessage) {
				var env map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out, &env))
				var detailStr string
				require.NoError(t, json.Unmarshal(env["detail"], &detailStr))
				var detail map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(detailStr), &detail))
				assert.NotContains(t, detail, datadogCarrierKey)
				var foo string
				require.NoError(t, json.Unmarshal(detail["foo"], &foo))
				assert.Equal(t, "bar", foo)
			},
		},
		{
			name:    "strips scheduled EventBridge with _datadog",
			enabled: true,
			fixture: "eventbridge-scheduled-with-datadog.json",
			verify: func(t *testing.T, in, out json.RawMessage) {
				var env map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out, &env))
				var detail map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(env["detail"], &detail))
				assert.NotContains(t, detail, datadogCarrierKey)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := loadFixture(t, tc.fixture)
			out := strip(tc.enabled, in)
			tc.verify(t, in, out)
		})
	}
}

func loadFixture(tb testing.TB, name string) json.RawMessage {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", name))
	require.NoError(tb, err)
	if name == "invalid.json" {
		return json.RawMessage(data)
	}
	var msg json.RawMessage
	require.NoError(tb, json.Unmarshal(data, &msg))
	return msg
}

func BenchmarkStripInjectedContext(b *testing.B) {
	cases := []struct {
		name    string
		enabled bool
		fixture string
	}{
		{name: "disabled", enabled: false, fixture: "eventbridge-with-datadog-object.json"},
		{name: "enabled_strip", enabled: true, fixture: "eventbridge-with-datadog-object.json"},
		{name: "enabled_noop_sqs", enabled: true, fixture: "sqs-event.json"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			msg := loadFixture(b, tc.fixture)
			l := MakeListener(Config{StripInjectedContext: tc.enabled})
			b.ReportAllocs()
			for b.Loop() {
				l.HandlerStarted(context.Background(), msg)
			}
		})
	}
}

func FuzzStripInjectedContext(f *testing.F) {
	f.Add(`{"detail":{"_datadog":{"x-datadog-trace-id":"1"},"foo":"bar"}}`)
	f.Add(`{"detail":"{\"foo\":\"bar\",\"_datadog\":{\"k\":\"v\"}}"}`)
	f.Add(`{"foo":"a,}b","_datadog":{"x":1}}`)
	f.Add(`{"detail":"{\"foo\":\"bar\",\"_datadog\":{\"k\":\"v}z\"}}"}`)
	f.Add(`{"_datadog":{},"_datadog":{}}`) // regression: adjacent carriers
	f.Add(`not even json`)
	f.Add(`{}`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, in string) {
		l := MakeListener(Config{StripInjectedContext: true})
		_, out := l.HandlerStarted(context.Background(), json.RawMessage(in))

		// Invariant 1: valid JSON in → valid JSON out (fail-open guarantee)
		if json.Valid([]byte(in)) && !json.Valid(out) {
			t.Fatalf("valid JSON became invalid: in=%q out=%q", in, out)
		}

		// Invariant 2: idempotent
		_, out2 := l.HandlerStarted(context.Background(), out)
		if !bytes.Equal(out, out2) {
			t.Fatalf("not idempotent: once=%q twice=%q", out, out2)
		}
	})
}
