// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentOmitsLangInV1Stats(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		// In the affected window: v1 support (and the defect) landed in
		// 7.73.0, and the fix was backported only to 7.79.x starting at
		// 7.79.0-rc.6.
		{"7.73.0", true},
		{"7.77.0", true},
		{"7.77.0-rc.1", true},
		{"7.78.2", true},
		{"7.78.0-devel+git.42.abcdef1", true},
		{"7.79.0-rc.4", true},
		{"v7.77.0", true}, // already-prefixed input handled
		{"7.77", true},    // semver shorthand for 7.77.0; no real agent emits this

		// Fixed.
		{"7.79.0-rc.6", false},
		{"7.79.0", false},
		{"7.80.1", false},

		// Below the lower bound: v1 either doesn't exist yet or isn't
		// advertised (gated behind apm_config.enable_v1_trace_endpoint), so
		// the protocol guard already excludes these; the version check
		// rejects them too.
		{"7.72.9", false},
		{"6.0.0", false}, // unldflagged fallback

		// Unparseable: must fail open (never treated as affected).
		{"", false},
		{"dev", false},
		{"datadogexporter-otelcol-0.155.0", false},
		{"7.77.0.1", false},
		{"08.77.0", false},
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			assert.Equal(t, tc.want, agentOmitsLangInV1Stats(tc.version))
		})
	}
}
