// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const datadogCarrierKey = "_datadog"

func loadTestJSON(t *testing.T, name string) json.RawMessage {
	t.Helper()

	bytes, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	var msg json.RawMessage
	require.NoError(t, json.Unmarshal(bytes, &msg))
	return msg
}

func mustLoadTestJSON(b *testing.B, name string) json.RawMessage {
	b.Helper()

	bytes, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		b.Fatal(err)
	}

	var msg json.RawMessage
	if err := json.Unmarshal(bytes, &msg); err != nil {
		b.Fatal(err)
	}
	return msg
}

func TestStripInjectedContext(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		fixture      string
		wantStrip    bool
		wantNoop     bool
		assertFooBar bool
	}{
		{
			name:         "enabled strips object detail",
			envValue:     "true",
			fixture:      "eventbridge-with-datadog-object.json",
			wantStrip:    true,
			assertFooBar: true,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetStripInjectedContextCacheForTest()
			t.Setenv(stripInjectedContextEnvVar, tt.envValue)

			var in json.RawMessage
			if tt.fixture == "invalid.json" {
				in = json.RawMessage(loadTestFileBytes(t, tt.fixture))
			} else {
				in = loadTestJSON(t, tt.fixture)
			}
			out := StripInjectedContext(in)

			if tt.wantNoop {
				assert.Equal(t, string(in), string(out))
				return
			}

			require.True(t, tt.wantStrip)
			assertDetailHasNoDatadog(t, out)
			if tt.assertFooBar {
				assertDetailContains(t, out, "foo", "bar")
			}
		})
	}
}

func TestStripInjectedContext_preservesOtherEnvelopeFields(t *testing.T) {
	ResetStripInjectedContextCacheForTest()
	t.Setenv(stripInjectedContextEnvVar, "true")

	in := loadTestJSON(t, "eventbridge-with-datadog-object.json")
	out := StripInjectedContext(in)

	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &envelope))

	assert.Equal(t, `"trace-propagation-test"`, string(envelope["detail-type"]))
	assert.Equal(t, `"trace-propagation.client"`, string(envelope["source"]))
	assert.Equal(t, `"test-event-id"`, string(envelope["id"]))
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

	var detailStrings map[string]string
	detailBytes, err := json.Marshal(detail)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(detailBytes, &detailStrings))
	assert.Equal(t, want, detailStrings[key])
}

func BenchmarkStripInjectedContext_disabled(b *testing.B) {
	ResetStripInjectedContextCacheForTest()
	b.Setenv(stripInjectedContextEnvVar, "false")
	msg := mustLoadTestJSON(b, "eventbridge-with-datadog-object.json")

	for b.Loop() {
		StripInjectedContext(msg)
	}
}

func BenchmarkStripInjectedContext_enabled_strip(b *testing.B) {
	ResetStripInjectedContextCacheForTest()
	b.Setenv(stripInjectedContextEnvVar, "true")
	msg := mustLoadTestJSON(b, "eventbridge-with-datadog-object.json")

	for b.Loop() {
		StripInjectedContext(msg)
	}
}

func BenchmarkStripInjectedContext_enabled_noop_sqs(b *testing.B) {
	ResetStripInjectedContextCacheForTest()
	b.Setenv(stripInjectedContextEnvVar, "true")
	msg := mustLoadTestJSON(b, "sqs-event.json")

	for b.Loop() {
		StripInjectedContext(msg)
	}
}

func loadTestFileBytes(t *testing.T, name string) []byte {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return bytes
}

func TestStripInjectedContext_regression(t *testing.T) {
	ResetStripInjectedContextCacheForTest()
	t.Setenv(stripInjectedContextEnvVar, "true")

	// Bug E
	in := json.RawMessage(`{"foo":"a,}b","_datadog":{"x":1}}`)
	out := StripInjectedContext(in)

	var obj map[string]string
	require.NoError(t, json.Unmarshal(out, &obj))
	assert.Equal(t, "a,}b", obj["foo"])
	assert.NotContains(t, obj, datadogCarrierKey)
}
