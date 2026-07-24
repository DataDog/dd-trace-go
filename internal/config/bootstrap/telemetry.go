// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package bootstrap owns configuration reads needed before the configuration
// provider and telemetry reporter can be constructed.
package bootstrap

import (
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal"
)

var (
	telemetryEnabled     bool
	telemetryEnabledOnce sync.Once
	telemetryDisabled    atomic.Bool
)

// TelemetryEnabled reports whether instrumentation telemetry is enabled.
// The environment is read at most once because telemetry may be initialized
// before the full configuration provider.
func TelemetryEnabled() bool {
	if telemetryDisabled.Load() {
		return false
	}
	telemetryEnabledOnce.Do(func() {
		telemetryEnabled = internal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true)
	})
	return telemetryEnabled && !telemetryDisabled.Load()
}

// Disable permanently disables telemetry after a fatal telemetry failure.
func Disable() {
	telemetryDisabled.Store(true)
}

// ResetForTesting clears the cached bootstrap configuration.
func ResetForTesting() {
	telemetryEnabled = false
	telemetryEnabledOnce = sync.Once{}
	telemetryDisabled.Store(false)
}
