// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfigAgentlessURL(t *testing.T) {
	for _, test := range []struct {
		name     string
		site     string
		config   ClientConfig
		expected string
	}{
		{
			name:     "default-site",
			expected: "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry",
		},
		{
			name:     "eu-site",
			site:     "datadoghq.eu",
			expected: "https://instrumentation-telemetry-intake.datadoghq.eu/api/v2/apmtelemetry",
		},
		{
			name:     "explicit-url-takes-precedence",
			site:     "datadoghq.eu",
			config:   ClientConfig{AgentlessURL: "https://custom.example.com/api/v2/apmtelemetry"},
			expected: "https://custom.example.com/api/v2/apmtelemetry",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.site != "" {
				t.Setenv("DD_SITE", test.site)
			}
			config := defaultConfig(test.config)
			assert.Equal(t, test.expected, config.AgentlessURL)
		})
	}
}
