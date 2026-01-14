// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap

import (
	"go.uber.org/zap/zapcore"
)

var _ zapcore.Core = (*traceCore)(nil)

// traceCore is a zapcore.Core wrapper that can be used for trace context injection.
// Note: Automatic trace context injection via GLS is not currently supported for zap
// because zap's Write method does not receive a context.Context.
// Use WithTraceFields(ctx, logger) for manual trace context injection.
type traceCore struct {
	zapcore.Core
}

// WrapCore wraps a zapcore.Core. This is a hook for potential future enhancements.
// Currently, it simply returns a wrapped core that passes through to the underlying core.
func WrapCore(core zapcore.Core) zapcore.Core {
	if _, ok := core.(*traceCore); ok {
		return core // Already wrapped
	}
	return &traceCore{Core: core}
}

// IsAlreadyWrapped checks whether the given core is already wrapped by this package.
func IsAlreadyWrapped(core zapcore.Core) bool {
	_, ok := core.(*traceCore)
	return ok
}

// With creates a new core with additional fields.
func (c *traceCore) With(fields []zapcore.Field) zapcore.Core {
	return &traceCore{Core: c.Core.With(fields)}
}

// Check determines whether the supplied Entry should be logged.
func (c *traceCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Core.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

// Write serializes the Entry and any Fields supplied at the log site and
// writes them to their destination.
func (c *traceCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	return c.Core.Write(ent, fields)
}

// Sync flushes buffered logs (if any).
func (c *traceCore) Sync() error {
	return c.Core.Sync()
}
