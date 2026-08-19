// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeHTTPMethod(t *testing.T) {
	for _, method := range []string{
		"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "QUERY", "TRACE",
	} {
		t.Run(method, func(t *testing.T) {
			attribute, spanName, original := NormalizeHTTPMethod(method)
			assert.Equal(t, method, attribute)
			assert.Equal(t, method, spanName)
			assert.Empty(t, original)
		})
	}

	tests := []struct {
		name      string
		method    string
		attribute string
		spanName  string
		original  string
	}{
		{name: "case variant", method: "gEt", attribute: "GET", spanName: "GET", original: "gEt"},
		{name: "unknown", method: "PROPFIND", attribute: "_OTHER", spanName: "HTTP", original: "PROPFIND"},
		{name: "empty", attribute: "_OTHER", spanName: "HTTP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attribute, spanName, original := NormalizeHTTPMethod(tt.method)
			assert.Equal(t, tt.attribute, attribute)
			assert.Equal(t, tt.spanName, spanName)
			assert.Equal(t, tt.original, original)
		})
	}
}
