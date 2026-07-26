// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"sync/atomic"
)

// EnvironmentConfig contains the process configuration values consumed by the
// telemetry package. internal/config supplies this value after loading the
// process-wide singleton; the small handoff avoids an import cycle.
type EnvironmentConfig struct {
	Debug                                     bool
	Site                                      string
	APIKey                                    string
	HeartbeatInterval                         float64
	HeartbeatIntervalSet                      bool
	DependencyCollectionEnabled               bool
	MetricsEnabled                            bool
	LogCollectionEnabled                      bool
	ExtendedHeartbeatInterval                 float64
	ExtendedHeartbeatIntervalSet              bool
	APISecurityEndpointCollectionMessageLimit int
}

var (
	environmentConfig               atomic.Pointer[EnvironmentConfig]
	instrumentationTelemetryEnabled atomic.Bool
)

func init() {
	instrumentationTelemetryEnabled.Store(true)
}

// SetInstrumentationTelemetryEnabled updates the singleton-backed telemetry
// enablement flag. It is separate from ConfigureEnvironment so config loading
// can apply it before reporting the remaining configuration values.
func SetInstrumentationTelemetryEnabled(enabled bool) {
	instrumentationTelemetryEnabled.Store(enabled)
}

// ConfigureEnvironment supplies the singleton-backed values used by telemetry.
func ConfigureEnvironment(config EnvironmentConfig) {
	environmentConfig.Store(&config)
}

func currentEnvironmentConfig() EnvironmentConfig {
	if config := environmentConfig.Load(); config != nil {
		return *config
	}
	return EnvironmentConfig{
		Site:                        "datadoghq.com",
		DependencyCollectionEnabled: true,
		MetricsEnabled:              true,
		LogCollectionEnabled:        true,
		APISecurityEndpointCollectionMessageLimit: 300,
	}
}
