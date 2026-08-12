// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

var testcases = []struct {
	name   string
	msg    string
	expect string
}{
	{
		"detail-no-dd-0",
		`{"detail": {}}`,
		`{"detail": {}}`,
	},
	{
		"detail-no-dd-1",
		`{"detail": {"foo": "bar"}}`,
		`{"detail": {"foo": "bar"}}`,
	},
	{
		"detail-no-dd-2",
		`{"detail": {"foo": "bar", "baz": "qux"}}`,
		`{"detail": {"foo": "bar", "baz": "qux"}}`,
	},
	{
		"detail-dd-object-alone",
		`{"detail": {"_datadog": {"x": "y"}}}`,
		`{"detail": {}}`,
	},
	{
		"detail-dd-string-alone",
		`{"detail": {"_datadog": "{\"x\": \"y\"}"}}`,
		`{"detail": {}}`,
	},
	{
		"detail-dd-object-beginning",
		`{"detail": {"_datadog": {"x": "y"}, "foo": "bar"}}`,
		`{"detail": {"foo": "bar"}}`,
	},
	{
		"detail-dd-string-beginning",
		`{"detail": {"_datadog": "{\"x\": \"y\"}", "foo": "bar"}}`,
		`{"detail": {"foo": "bar"}}`,
	},
	{
		"detail-dd-object-end",
		`{"detail": {"foo": "bar", "_datadog": {"x": "y"}}}`,
		`{"detail": {"foo": "bar"}}`,
	},
	{
		"detail-dd-string-end",
		`{"detail": {"foo": "bar", "_datadog": "{\"x\": \"y\"}"}}`,
		`{"detail": {"foo": "bar"}}`,
	},
	{
		"detail-dd-object-middle",
		`{"detail": {"foo": "bar", "_datadog": {"x": "y"}, "baz": "qux"}}`,
		`{"detail": {"foo": "bar", "baz": "qux"}}`,
	},
	{
		"detail-dd-string-middle",
		`{"detail": {"foo": "bar", "_datadog": "{\"x\": \"y\"}", "baz": "qux"}}`,
		`{"detail": {"foo": "bar", "baz": "qux"}}`,
	},
	{
		"bad-too-many-brackets",
		`{"detail": {"_datadog": "{{}}"}}`,
		`{"detail": {"_datadog": "{{}}"}}`,
	},
	{
		"bad-missing-trailing-quote",
		`{"detail": {"_datadog": "{}}}`,
		`{"detail": {"_datadog": "{}}}`,
	},
	{
		"closebrace-before-openbrace",
		`{"detail": {"_datadog": "}"}}`,
		`{"detail": {}}`,
	},
	{
		"no-whitespace",
		`{"detail":{"_datadog":{"x":"y"}}}`,
		`{"detail":{}}`,
	},
}

func TestStripInjectedContext(t *testing.T) {
	for _, fixture := range testcases {
		t.Run(fixture.name, func(t *testing.T) {
			msg := json.RawMessage(fixture.msg)
			stripped := StripInjectedContext(msg)
			assert.Equal(t, fixture.expect, string(stripped))
		})
	}
}

func BenchmarkStripInjectedContext(b *testing.B) {
	for _, fixture := range testcases {
		b.Run(fixture.name, func(b *testing.B) {
			msg := json.RawMessage(fixture.msg)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				StripInjectedContext(msg)
			}
		})
	}
}
