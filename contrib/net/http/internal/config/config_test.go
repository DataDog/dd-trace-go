// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientErrorCheck(t *testing.T) {
	tests := []struct {
		name         string
		otel         bool
		config       string
		errorCodes   []int
		successCodes []int
	}{
		{
			name:         "Datadog default",
			errorCodes:   []int{400, 499},
			successCodes: []int{-1, 0, 99, 399, 500, 600},
		},
		{
			name:         "OpenTelemetry default",
			otel:         true,
			errorCodes:   []int{-1, 0, 99, 400, 500, 599, 600},
			successCodes: []int{100, 399},
		},
		{
			name:         "custom range",
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
