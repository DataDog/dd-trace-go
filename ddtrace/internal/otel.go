// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package internal

import "sync/atomic"

// otelTracingAPIEnabled reports whether the OpenTelemetry tracing API bridge is
// in use. Go enables the bridge in code, by creating a ddtrace/opentelemetry
// TracerProvider, so this is its equivalent of DD_TRACE_OTEL_ENABLED.
var otelTracingAPIEnabled atomic.Bool

// SetOTelTracingAPIEnabled records whether the OpenTelemetry tracing API bridge
// is in use.
func SetOTelTracingAPIEnabled(enabled bool) {
	otelTracingAPIEnabled.Store(enabled)
}

// OTelTracingAPIEnabled reports whether the OpenTelemetry tracing API bridge is
// in use.
func OTelTracingAPIEnabled() bool {
	return otelTracingAPIEnabled.Load()
}
