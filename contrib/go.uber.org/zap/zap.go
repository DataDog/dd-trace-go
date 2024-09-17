// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package zap provides functions to correlate logs and traces using go.uber.org/zap package (https://github.com/uber-go/zap).
package zap // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/go.uber.org/zap"

import (
	"context"
	"go.uber.org/zap"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "go.uber.org/zap"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// WithTraceFields looks for an active span in the provided context and if found, it returns a new *zap.Logger with
// traceID and spanID keys set.
func WithTraceFields(ctx context.Context, logger *zap.Logger) *zap.Logger {
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		traceID := strconv.FormatUint(span.Context().TraceID(), 10)
		spanID := strconv.FormatUint(span.Context().SpanID(), 10)

		return logger.With(
			zap.String(ext.LogKeyTraceID, traceID),
			zap.String(ext.LogKeySpanID, spanID),
		)
	}
	return logger
}
