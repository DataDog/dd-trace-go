// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/puzpuzpuz/xsync/v3"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

type loggerKey struct {
	tags    string
	message string
	level   LogLevel
}

type loggerValue struct {
	count             atomic.Uint32
	record            slog.Record
	captureStacktrace bool
}

type loggerBackend struct {
	store *xsync.MapOf[loggerKey, *loggerValue]

	distinctLogs       atomic.Int32
	maxDistinctLogs    int32
	onceMaxLogsReached sync.Once
}

func (logger *loggerBackend) Add(record slog.Record, opts ...LogOption) {
	if logger.distinctLogs.Load() >= logger.maxDistinctLogs {
		logger.onceMaxLogsReached.Do(func() {
			logger.add(newRecord(LogError, "telemetry: log count exceeded maximum, dropping log"), WithStacktrace())
		})
		return
	}

	logger.add(record, opts...)
}

func (logger *loggerBackend) add(record slog.Record, opts ...LogOption) {
	key := loggerKey{
		level:   slogLevelToLogLevel(record.Level),
		message: record.Message,
	}

	for _, opt := range opts {
		opt(&key, nil)
	}

	value, _ := logger.store.LoadOrCompute(key, func() *loggerValue {
		// Create the record at capture time, not send time
		value := &loggerValue{
			record: record,
		}
		for _, opt := range opts {
			opt(nil, value)
		}
		logger.distinctLogs.Add(1)
		return value
	})

	value.count.Add(1)
}

// TODO: Explore using internal/stacktrace/stacktrace.go for stack unwinding
func unwindStackFromPC(pc uintptr) string {
	if pc == 0 {
		return ""
	}

	frames := runtime.CallersFrames([]uintptr{pc})
	var stackTrace []byte
	for {
		frame, more := frames.Next()
		if len(stackTrace) > 0 {
			stackTrace = append(stackTrace, '\n')
		}
		stackTrace = append(stackTrace, frame.Function...)
		stackTrace = append(stackTrace, '\n', '\t')
		stackTrace = append(stackTrace, frame.File...)
		stackTrace = append(stackTrace, ':')
		// Simple integer to string conversion
		line := frame.Line
		if line == 0 {
			stackTrace = append(stackTrace, '0')
		} else {
			digits := make([]byte, 0, 10)
			for line > 0 {
				digits = append(digits, byte('0'+line%10))
				line /= 10
			}
			// Reverse digits
			for i := len(digits) - 1; i >= 0; i-- {
				stackTrace = append(stackTrace, digits[i])
			}
		}
		if !more {
			break
		}
	}
	return string(stackTrace)
}

func (logger *loggerBackend) Payload() transport.Payload {
	logs := make([]transport.LogMessage, 0, logger.store.Size()+1)
	logger.store.Range(func(key loggerKey, value *loggerValue) bool {
		logger.store.Delete(key)
		logger.distinctLogs.Add(-1)
		msg := transport.LogMessage{
			Message:    key.message,
			Level:      key.level,
			Tags:       key.tags,
			Count:      value.count.Load(),
			TracerTime: value.record.Time.Unix(),
		}
		if value.captureStacktrace {
			msg.StackTrace = unwindStackFromPC(value.record.PC)
		}
		logs = append(logs, msg)
		return true
	})

	if len(logs) == 0 {
		return nil
	}

	return transport.Logs{Logs: logs}
}
