// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package bootstrap owns configuration reads needed before the configuration
// provider and telemetry reporter can be constructed.
package bootstrap

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
		raw, present := env.Lookup("DD_INSTRUMENTATION_TELEMETRY_ENABLED")
		state.enabled = parseTelemetryEnabledValue(raw, present)
	})
	return state.enabled && !state.disabled.Load()
}

func parseTelemetryEnabledValue(raw string, present bool) bool {
	if !present {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		log.Warn("Non-boolean value for env var %s. Parse failed with error: %v", "DD_INSTRUMENTATION_TELEMETRY_ENABLED", err.Error())
		return true
	}
	return enabled
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
