// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetStripInjectedContextCacheForTest() {
	// This resets the sync.Once env cache between tests.
	stripInjectedContextEnabledOnce = sync.Once{}
	stripInjectedContextEnabledVal = false
}

func loadFixture(tb testing.TB, name string) json.RawMessage {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(tb, err)
	if name == "invalid.json" {
		return json.RawMessage(data) // raw bytes for fail-open
	}
	var msg json.RawMessage
	require.NoError(tb, json.Unmarshal(data, &msg))
	return msg
}

func TestStripInjectedContext(t *testing.T) {
	tests := []struct {
		name          string
		envValue      string
		fixture       string
		wantStrip     bool
		wantNoop      bool
		assertFooBar  bool
		inline        string
		checkEnvelope bool
		verify        func(t *testing.T, out json.RawMessage)
	}{
		{
			name:          "enabled strips object detail",
			envValue:      "true",
			fixture:       "eventbridge-with-datadog-object.json",
			wantStrip:     true,
			assertFooBar:  true,
			checkEnvelope: true,
		},
		{
			name:         "enabled strips string detail",
			envValue:     "true",
			fixture:      "eventbridge-with-datadog-string-detail.json",
			wantStrip:    true,
			assertFooBar: true,
		},
		{
			name:     "disabled leaves payload unchanged",
			envValue: "false",
			fixture:  "eventbridge-with-datadog-object.json",
			wantNoop: true,
		},
		{
			name:     "unset leaves payload unchanged",
			envValue: "",
			fixture:  "eventbridge-with-datadog-object.json",
			wantNoop: true,
		},
		{
			name:     "enabled no-op without datadog key",
			envValue: "true",
			fixture:  "eventbridge-without-datadog.json",
			wantNoop: true,
		},
		{
			name:         "enabled strips scheduled events with datadog",
			envValue:     "true",
			fixture:      "eventbridge-scheduled-with-datadog.json",
			wantStrip:    true,
			assertFooBar: false,
		},
		{
			name:     "enabled no-op for sqs",
			envValue: "true",
			fixture:  "sqs-event.json",
			wantNoop: true,
		},
		{
			name:     "enabled no-op for invalid json",
			envValue: "true",
			fixture:  "invalid.json",
			wantNoop: true,
		},
		{
			name:     "regression object detail Bug E",
			envValue: "true",
			inline:   `{"foo":"a,}b","_datadog":{"x":1}}`,
			verify:   verifyBugEObject,
		},
		{
			name:     "regression string detail Bug E",
			envValue: "true",
			inline:   `{"detail":"{\"foo\":\"bar\",\"_datadog\":{\"k\":\"v}z\"}}"}`,
			verify:   verifyBugEStringDetail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetStripInjectedContextCacheForTest()
			t.Setenv(StripInjectedContextEnvVar, tt.envValue)

			var in json.RawMessage
			switch {
			case tt.inline != "":
				in = json.RawMessage(tt.inline)
			default:
				in = loadFixture(t, tt.fixture)
			}
			out := StripInjectedContext(in)

			if tt.verify != nil {
				tt.verify(t, out)
				return
			}
			if tt.wantNoop {
				assert.Equal(t, string(in), string(out))
				return
			}

			require.True(t, tt.wantStrip)
			assertDetailHasNoDatadog(t, out)
			if tt.assertFooBar {
				assertDetailContains(t, out, "foo", "bar")
			}
			if tt.checkEnvelope {
				assertEnvelopeFields(t, out)
			}
		})
	}
}

func detailAsMap(t *testing.T, detailRaw json.RawMessage) map[string]json.RawMessage {
	t.Helper()

	var detailObj map[string]json.RawMessage
	if err := json.Unmarshal(detailRaw, &detailObj); err == nil {
		return detailObj
	}

	var detailStr string
	require.NoError(t, json.Unmarshal(detailRaw, &detailStr))
	require.NoError(t, json.Unmarshal([]byte(detailStr), &detailObj))
	return detailObj
}

func assertDetailHasNoDatadog(t *testing.T, out json.RawMessage) {
	t.Helper()

	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &envelope))

	detail := detailAsMap(t, envelope["detail"])
	assert.NotContains(t, detail, datadogCarrierKey)
}

func assertDetailContains(t *testing.T, out json.RawMessage, key, want string) {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &envelope))
	detail := detailAsMap(t, envelope["detail"])

	var s string
	require.NoError(t, json.Unmarshal(detail[key], &s))
	assert.Equal(t, want, s)
}

func assertEnvelopeFields(t *testing.T, out json.RawMessage) {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &envelope))
	assert.Equal(t, `"trace-propagation-test"`, string(envelope["detail-type"]))
	assert.Equal(t, `"trace-propagation.client"`, string(envelope["source"]))
	assert.Equal(t, `"test-event-id"`, string(envelope["id"]))
}

func verifyBugEObject(t *testing.T, out json.RawMessage) {
	t.Helper()
	var obj map[string]string
	require.NoError(t, json.Unmarshal(out, &obj))
	assert.Equal(t, "a,}b", obj["foo"])
	assert.NotContains(t, obj, datadogCarrierKey)
}

func BenchmarkStripInjectedContext(b *testing.B) {
	cases := []struct {
		name    string
		env     string
		fixture string
	}{
		{
			name:    "disabled",
			env:     "false",
			fixture: "eventbridge-with-datadog-object.json",
		},
		{
			name:    "enabled_strip",
			env:     "true",
			fixture: "eventbridge-with-datadog-object.json",
		},
		{
			name:    "enabled_noop_sqs",
			env:     "true",
			fixture: "sqs-event.json",
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			resetStripInjectedContextCacheForTest()
			b.Setenv(StripInjectedContextEnvVar, tc.env)
			msg := loadFixture(b, tc.fixture)

			b.ReportAllocs()
			for b.Loop() {
				StripInjectedContext(msg)
			}
		})
	}
}

func verifyBugEStringDetail(t *testing.T, out json.RawMessage) {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &envelope))
	var detailStr string
	require.NoError(t, json.Unmarshal(envelope["detail"], &detailStr))
	var detail map[string]string
	require.NoError(t, json.Unmarshal([]byte(detailStr), &detail))
	assert.Equal(t, "bar", detail["foo"])
	assert.NotContains(t, detail, datadogCarrierKey)
}
