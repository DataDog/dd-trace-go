// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package zap provides functions to correlate logs and traces using the go.uber.org/zap package (https://github.com/uber-go/zap).
package zap

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoUberOrgZap)
}

type config struct {
	log128bits bool
}

var cfg = newConfig()

func newConfig() *config {
	return &config{
		log128bits: options.GetBoolEnv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", true),
	}
}

// TraceFields returns zap fields containing the Datadog trace and span IDs
// from the given context. If no active span is found in the context, it
// returns nil.
func TraceFields(ctx context.Context) []zap.Field {
	span, found := tracer.SpanFromContext(ctx)
	if !found {
		return nil
	}
	var traceID string
	if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
		traceID = span.Context().TraceID()
	} else {
		traceID = strconv.FormatUint(span.Context().TraceIDLower(), 10)
	}
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)
	return []zap.Field{
		zap.String(ext.LogKeyTraceID, traceID),
		zap.String(ext.LogKeySpanID, spanID),
	}
}
