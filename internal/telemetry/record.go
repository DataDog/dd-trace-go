// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"log/slog"
	"runtime"
)

const defaultSkipFrames = 3

// capturePC captures the program counter of the current function.
func capturePC() uintptr {
	// Lazy stack unwinding - only capture when actually needed
	// Skip the appropriate number of frames:
	// [runtime.Callers, capturePC, caller's logging method, user code]
	var pcs [1]uintptr
	runtime.Callers(defaultSkipFrames, pcs[:])
	return pcs[0]
}

func logLevelToSlogLevel(level LogLevel) slog.Level {
	switch level {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelError
	}
}

func slogLevelToLogLevel(level slog.Level) LogLevel {
	switch level {
	case slog.LevelDebug:
		return LogDebug
	case slog.LevelWarn:
		return LogWarn
	case slog.LevelError:
		return LogError
	default:
		return LogError
	}
}

func newRecord(level LogLevel, message string) slog.Record {
	return slog.Record{
		Level:   logLevelToSlogLevel(level),
		Message: message,
		PC:      capturePC(),
	}
}
