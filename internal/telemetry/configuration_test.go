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

func TestConfigurationInvertedArrivalRetainsNewerSameKey(t *testing.T) {
	c := &configuration{}
	// These are the exact arrival semantics produced when a newer prepared
	// Config setter report reaches the sink before an older blocked report.
	c.Add(Configuration{Name: "DD_SERVICE", Value: "new", Origin: OriginCode, SeqID: 20})
	c.Add(Configuration{Name: "DD_SERVICE", Value: "old", Origin: OriginCode, SeqID: 19})

	configs := c.All()
	if assert.Len(t, configs, 1) {
		assert.Equal(t, "new", configs[0].Value)
		assert.Equal(t, uint64(20), configs[0].SeqID)
	}
}

func TestConfigurationLegacySequenceResetsExplicitOrderingDomain(t *testing.T) {
	c := &configuration{}
	key := Configuration{Name: "DD_SERVICE", Origin: OriginCode}

	legacy := key
	legacy.Value = "legacy-first"
	c.Add(legacy)
	_ = c.Payload() // Assign a fallback sequence to the retained legacy entry.

	explicit := key
	explicit.Value = "explicit-high"
	explicit.SeqID = 100
	c.Add(explicit)

	legacy.Value = "legacy-later"
	c.Add(legacy)
	legacyPayload := c.Payload().(transport.AppClientConfigurationChange)
	if assert.Len(t, legacyPayload.Configuration, 1) {
		assert.Equal(t, "legacy-later", legacyPayload.Configuration[0].Value)
		assert.NotZero(t, legacyPayload.Configuration[0].SeqID)
	}

	explicit.Value = "explicit-after-legacy"
	explicit.SeqID = 10
	c.Add(explicit)
	explicit.Value = "explicit-stale"
	explicit.SeqID = 9
	c.Add(explicit)

	configs := c.All()
	if assert.Len(t, configs, 1) {
		assert.Equal(t, "explicit-after-legacy", configs[0].Value)
		assert.Equal(t, uint64(10), configs[0].SeqID)
	}
}

func TestConfigurationEqualExplicitSequencePreservesArrivalBehavior(t *testing.T) {
	c := &configuration{}
	c.Add(Configuration{Name: "DD_SERVICE", Value: "first", Origin: OriginDefault, SeqID: 1})
	c.Add(Configuration{Name: "DD_SERVICE", Value: "second", Origin: OriginDefault, SeqID: 1})

	configs := c.All()
	if assert.Len(t, configs, 1) {
		assert.Equal(t, "second", configs[0].Value)
	}
}
