// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"sync"
	"sync/atomic"
)

var (
	instrumentationTelemetryLoader  atomic.Value
	instrumentationTelemetryEnabled atomic.Bool
	instrumentationTelemetryOnce    sync.Once
)

type instrumentationTelemetryEnabledLoader func() bool

func init() {
	instrumentationTelemetryLoader.Store(instrumentationTelemetryEnabledLoader(func() bool { return true }))
	instrumentationTelemetryEnabled.Store(true)
}

// SetInstrumentationTelemetryEnabledLoader supplies the singleton-owned loader
// without introducing an import cycle. The loader runs at most once, when
// instrumentation telemetry is first queried.
func SetInstrumentationTelemetryEnabledLoader(loader func() bool) {
	instrumentationTelemetryLoader.Store(instrumentationTelemetryEnabledLoader(loader))
}

func setInstrumentationTelemetryEnabled(enabled bool) {
	SetInstrumentationTelemetryEnabledLoader(func() bool { return enabled })
}

// ResolveInstrumentationTelemetryEnabled resolves and caches the singleton
// value. The supplied loader is used only if this is the first resolution.
func ResolveInstrumentationTelemetryEnabled(loader func() bool) bool {
	instrumentationTelemetryOnce.Do(func() {
		instrumentationTelemetryEnabled.Store(loader())
	})
	return instrumentationTelemetryEnabled.Load()
}

func instrumentationTelemetryDisabled() bool {
	loader := instrumentationTelemetryLoader.Load().(instrumentationTelemetryEnabledLoader)
	return !ResolveInstrumentationTelemetryEnabled(loader)
}

func disableInstrumentationTelemetryAfterFailure() {
	instrumentationTelemetryEnabled.Store(false)
}
