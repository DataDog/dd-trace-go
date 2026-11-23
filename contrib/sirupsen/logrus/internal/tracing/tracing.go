// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package tracing holds the logrus log correlation logic. It must not import
// github.com/sirupsen/logrus, because Orchestrion injects code that calls it
// into that package.
package tracing

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

func init() {
	instrumentation.Load(instrumentation.PackageSirupsenLogrus)
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

// InjectTraceFields adds the trace and span IDs of the span in ctx to fields.
// It does nothing if ctx holds no span.
func InjectTraceFields(ctx context.Context, fields map[string]any) {
	span, found := tracer.SpanFromContext(ctx)
	if !found {
		return
	}
	if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
		fields[ext.LogKeyTraceID] = span.Context().TraceID()
	} else {
		fields[ext.LogKeyTraceID] = strconv.FormatUint(span.Context().TraceIDLower(), 10)
	}
	fields[ext.LogKeySpanID] = strconv.FormatUint(span.Context().SpanID(), 10)
}
