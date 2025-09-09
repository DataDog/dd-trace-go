// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"time"
)

// handlerWriter provides lazy evaluation logging with slog.Handler.
// It provides lazy stack unwinding - only capturing stack traces when
// the handler is enabled for the given level.
// Adapted from Go standard library's internal slog implementation for
// improved performance with deferred/lazy evaluation.
type handlerWriter struct {
	h         slog.Handler
	capturePC bool // Whether to capture program counter for stack traces
}

// NewHandlerWriter creates a new handlerWriter that writes to the given handler.
// The capturePC parameter controls whether to capture stack traces for log entries.
func NewHandlerWriter(h slog.Handler, capturePC bool) *handlerWriter {
	return &handlerWriter{
		h:         h,
		capturePC: capturePC,
	}
}

// LogRecord logs a slog.Record with lazy evaluation.
// It provides lazy stack unwinding - only capturing stack traces when
// the handler is enabled for the given level and PC is not already captured.
func (w *handlerWriter) LogRecord(record slog.Record) error {
	if !w.h.Enabled(context.Background(), record.Level) {
		return nil
	}

	// Add PC if needed and not already captured
	if w.capturePC && record.PC == 0 {
		// Skip the appropriate number of frames:
		// [runtime.Callers, w.LogRecord, caller's logging method, user code]
		var pcs [1]uintptr
		runtime.Callers(4, pcs[:])
		record.PC = pcs[0]
	}

	return w.h.Handle(context.Background(), record)
}

// Log logs a message at the specified level with lazy evaluation.
// It provides lazy stack unwinding - only capturing stack traces when
// the handler is enabled for the given level.
//
// This is the key performance optimization: we check if the handler is enabled
// before doing any expensive work like stack unwinding or message formatting.
func (w *handlerWriter) Log(level slog.Level, message string) error {
	// Early return if handler is not enabled for this level.
	// This is the key optimization - no work done for disabled levels.
	// This includes avoiding stack unwinding, time capture, and string processing.
	if !w.h.Enabled(context.Background(), level) {
		return nil
	}

	var pc uintptr
	if w.capturePC {
		// TODO: use capturePC from telemetry package
		// Lazy stack unwinding - only capture when actually needed
		// Skip the appropriate number of frames:
		// [runtime.Callers, w.Log, caller's logging method, user code]
		var pcs [1]uintptr
		runtime.Callers(4, pcs[:])
		pc = pcs[0]
	}

	// Create slog.Record with the message and optional PC
	// Time is captured here, only when the log will actually be processed
	r := slog.NewRecord(time.Now(), level, message, pc)

	// Pass to handler for processing
	return w.h.Handle(context.Background(), r)
}

func (w *handlerWriter) Write(buf []byte) (int, error) {
	origLen := len(buf)
	buf = bytes.TrimSuffix(buf, []byte{'\n'})
	return origLen, w.Log(slog.LevelInfo, string(buf))
}
