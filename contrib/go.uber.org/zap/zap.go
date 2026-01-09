// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package zap provides functions to correlate logs and traces using go.uber.org/zap package (https://github.com/uber-go/zap).
package zap // import "github.com/DataDog/dd-trace-go/v2/contrib/go.uber.org/zap"

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"go.uber.org/zap"
)

var instr *instrumentation.Instrumentation

type config struct {
	log128bits bool
}

var cfg = newConfig()

func newConfig() *config {
	return &config{
		log128bits: options.GetBoolEnv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", true),
	}
}

func init() {
	instr = instrumentation.Load(instrumentation.PackageUberZap)
}

// WithTraceFields looks for an active span in the provided context and if found, it returns a new *zap.Logger with
// traceID and spanID keys set.
func WithTraceFields(ctx context.Context, logger *zap.Logger) *zap.Logger {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return logger
	}

	var traceID string
	if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
		traceID = span.Context().TraceID()
	} else {
		traceID = strconv.FormatUint(span.Context().TraceIDLower(), 10)
	}

	spanID := strconv.FormatUint(span.Context().SpanID(), 10)

	return logger.With(
		zap.String(ext.LogKeyTraceID, traceID),
		zap.String(ext.LogKeySpanID, spanID),
	)
}
