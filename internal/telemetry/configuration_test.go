// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// TestConfigurationOmitsSensitive verifies that configurations marked sensitive are not
// stored or reported by the configuration data source, regardless of which reporting
// helper added them. Non-sensitive configurations are reported as usual.
func TestConfigurationOmitsSensitive(t *testing.T) {
	c := &configuration{}

	// Sensitive entries (OTLP header variants) must be dropped.
	c.Add(Configuration{Name: "OTEL_EXPORTER_OTLP_HEADERS", Value: "api-key=SENTINEL", Origin: OriginEnvVar})
	c.Add(Configuration{Name: "OTEL_EXPORTER_OTLP_TRACES_HEADERS", Value: "api-key=SENTINEL", Origin: OriginEnvVar})
	c.Add(Configuration{Name: "OTEL_EXPORTER_OTLP_METRICS_HEADERS", Value: "api-key=SENTINEL", Origin: OriginEnvVar})
	c.Add(Configuration{Name: "OTEL_EXPORTER_OTLP_LOGS_HEADERS", Value: "api-key=SENTINEL", Origin: OriginEnvVar})

	// A non-sensitive entry must be reported.
	c.Add(Configuration{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://localhost:4318", Origin: OriginEnvVar})

	payload := c.Payload()
	change, ok := payload.(transport.AppClientConfigurationChange)
	assert.True(t, ok, "expected an AppClientConfigurationChange payload")

	assertOnlyEndpoint(t, change.Configuration)
	// All() reflects the same accumulated state used by extended heartbeats.
	assertOnlyEndpoint(t, c.All())
}

func assertOnlyEndpoint(t *testing.T, configs []transport.ConfKeyValue) {
	t.Helper()
	names := make([]string, 0, len(configs))
	for _, cfg := range configs {
		names = append(names, cfg.Name)
		assert.NotContains(t, cfg.Name, "HEADERS", "header configuration should not be reported")
		if s, ok := cfg.Value.(string); ok {
			assert.NotContains(t, s, "SENTINEL", "no reported value may contain the sentinel")
		}
	}
	assert.Equal(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT"}, names)
}
