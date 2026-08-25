// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestDefaultCapturesOTelSemantics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "disabled", value: "false"},
		{name: "enabled", value: "true", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", tt.value)
			require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
			t.Cleanup(tracer.Stop)

			cfg := Default(Instrumentation)
			assert.Equal(t, tt.want, cfg.OTelSemanticsEnabled)
		})
	}
}

func TestNormalizeClientRequestMethod(t *testing.T) {
	for _, tt := range []struct {
		name      string
		method    string
		attribute string
		spanName  string
		original  string
	}{
		{name: "empty", attribute: http.MethodGet, spanName: http.MethodGet},
		{name: "canonical", method: http.MethodPost, attribute: http.MethodPost, spanName: http.MethodPost},
		{name: "case variant", method: "gEt", attribute: http.MethodGet, spanName: http.MethodGet, original: "gEt"},
		{name: "unknown", method: "PROPFIND", attribute: "_OTHER", spanName: "HTTP", original: "PROPFIND"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attribute, spanName, original := NormalizeClientRequestMethod(&http.Request{Method: tt.method})
			assert.Equal(t, tt.attribute, attribute)
			assert.Equal(t, tt.spanName, spanName)
			assert.Equal(t, tt.original, original)
		})
	}
}

func TestClientErrorCheckUsesModeSpecificDefaultsAndCustomRanges(t *testing.T) {
	tests := []struct {
		name         string
		otel         bool
		config       string
		errorCodes   []int
		successCodes []int
	}{
		{
			name:         "Datadog defaults mark only 400-499 as errors",
			errorCodes:   []int{400, 499},
			successCodes: []int{-1, 0, 99, 399, 500, 600},
		},
		{
			name:         "OpenTelemetry defaults use semantic status boundaries",
			otel:         true,
			errorCodes:   []int{-1, 0, 99, 400, 500, 599, 600},
			successCodes: []int{100, 399},
		},
		{
			name:         "custom range replaces defaults",
			otel:         true,
			config:       "500-510",
			errorCodes:   []int{500, 510},
			successCodes: []int{99, 400, 511, 600},
		},
		{
			name:         "malformed range uses OpenTelemetry default",
			otel:         true,
			config:       "invalid",
			errorCodes:   []int{99, 400, 500, 600},
			successCodes: []int{100, 399},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config != "" {
				t.Setenv(EnvClientErrorStatuses, tt.config)
			}
			check := ClientErrorCheck(tt.otel)
			for _, status := range tt.errorCodes {
				assert.True(t, check(status), "status %d", status)
			}
			for _, status := range tt.successCodes {
				assert.False(t, check(status), "status %d", status)
			}
		})
	}
}
