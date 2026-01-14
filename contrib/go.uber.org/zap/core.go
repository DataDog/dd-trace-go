// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var _ zapcore.Core = (*traceCore)(nil)

// traceCore is a zapcore.Core wrapper that automatically injects trace context
// from GLS (Go Local Storage) when built with Orchestrion.
type traceCore struct {
	zapcore.Core
}

// Wraps a zapcore.Core with automatic trace context injection.
// When the application is built with Orchestrion, this core will automatically
// add dd.trace_id and dd.span_id fields to log entries if an active span exists
// in the current goroutine's context.
func WrapCore(core zapcore.Core) zapcore.Core {
	if _, ok := core.(*traceCore); ok {
		return core // Already wrapped
	}
	return &traceCore{Core: core}
}

// Checks whether the given core is already wrapped by this package.
func IsAlreadyWrapped(core zapcore.Core) bool {
	_, ok := core.(*traceCore)
	return ok
}

// Creates a new core with additional fields.
func (c *traceCore) With(fields []zapcore.Field) zapcore.Core {
	return &traceCore{Core: c.Core.With(fields)}
}

// Returns whether the supplied Entry should be logged.
func (c *traceCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Core.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

// Serializes the Entry and any Fields supplied at the log site and
// writes them to their destination, injecting trace context if available.
func (c *traceCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	fields = c.withTraceFieldsFromGLS(fields)
	return c.Core.Write(ent, fields)
}

// Flushes buffered logs (if any)
func (c *traceCore) Sync() error {
	return c.Core.Sync()
}

// Adds trace context fields from GLS if orchestrion is enabled
// and an active span exists in the current goroutine.
func (c *traceCore) withTraceFieldsFromGLS(fields []zapcore.Field) []zapcore.Field {
	if !orchestrion.Enabled() {
		return fields
	}

	spanVal := orchestrion.GLSPeekValue(internal.ActiveSpanKey)
	if spanVal == nil {
		return fields
	}

	span, ok := spanVal.(*tracer.Span)
	if !ok || span == nil {
		return fields
	}

	spanCtx := span.Context()
	var traceID string
	if cfg.log128bits && spanCtx.TraceID() != tracer.TraceIDZero {
		traceID = spanCtx.TraceID()
	} else {
		traceID = strconv.FormatUint(spanCtx.TraceIDLower(), 10)
	}

	spanID := strconv.FormatUint(spanCtx.SpanID(), 10)

	return append(fields,
		zap.String(ext.LogKeyTraceID, traceID),
		zap.String(ext.LogKeySpanID, spanID),
	)
}
