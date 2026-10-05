// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
)

// ddAwareLogger wraps an OTel Logger and automatically bridges Datadog spans
// to OpenTelemetry span context before emitting logs. This ensures that logs
// emitted with a Datadog span in the context are correlated with the correct
// trace and span IDs.
//
// This type implements the otel/log.Logger interface and is not exported.
type ddAwareLogger struct {
	embedded.Logger
	underlying log.Logger
}

// Emit emits a log record with automatic Datadog span bridging.
// If the context contains a Datadog span but no OpenTelemetry span, it bridges
// the Datadog span to OpenTelemetry context before calling the underlying logger.
func (l *ddAwareLogger) Emit(ctx context.Context, record log.Record) {
	// Automatically bridge Datadog spans if present
	bridgedCtx := contextWithDDSpan(ctx)
	l.underlying.Emit(bridgedCtx, record)
}

// Enabled returns whether the logger is enabled for the given level and context.
func (l *ddAwareLogger) Enabled(ctx context.Context, param log.EnabledParameters) bool {
	// Bridge context for consistency (in case Enabled checks span context)
	ctx = contextWithDDSpan(ctx)
	return l.underlying.Enabled(ctx, param)
}

// newDDAwareLogger wraps an OpenTelemetry logger with automatic Datadog span bridging.
func newDDAwareLogger(underlying log.Logger) log.Logger {
	return &ddAwareLogger{underlying: underlying}
}
