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

var telemetryStatePointer atomic.Pointer[telemetryState]

type telemetryState struct {
	once     sync.Once
	enabled  bool
	disabled atomic.Bool
}

// TelemetryEnabled reports whether instrumentation telemetry is enabled.
// The environment is read at most once because telemetry may be initialized
// before the full configuration provider.
func TelemetryEnabled() bool {
	state := loadTelemetryState()
	if state.disabled.Load() {
		return false
	}
	state.once.Do(func() {
		state.enabled = internal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true)
	})
	return state.enabled && !state.disabled.Load()
}

// Disable permanently disables telemetry after a fatal telemetry failure.
func Disable() {
	loadTelemetryState().disabled.Store(true)
}

func loadTelemetryState() *telemetryState {
	state := telemetryStatePointer.Load()
	if state != nil {
		return state
	}
	state = new(telemetryState)
	if telemetryStatePointer.CompareAndSwap(nil, state) {
		return state
	}
	return telemetryStatePointer.Load()
}

// ResetForTesting clears the cached bootstrap configuration.
func ResetForTesting() {
	telemetryStatePointer.Store(new(telemetryState))
}
