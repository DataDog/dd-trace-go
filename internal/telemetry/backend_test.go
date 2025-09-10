// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

func TestLoggerBackend_formatMessage(t *testing.T) {
	backend := newLoggerBackend(100)

	tests := []struct {
		name           string
		setupRecord    func() slog.Record
		expectedOutput string
		description    string
	}{
		{
			name: "plain message without attributes",
			setupRecord: func() slog.Record {
				return slog.NewRecord(time.Now(), slog.LevelDebug, "simple message", 0)
			},
			expectedOutput: "simple message",
			description:    "Should return raw message when no attributes present",
		},
		{
			name: "message with single attribute",
			setupRecord: func() slog.Record {
				record := slog.NewRecord(time.Now(), slog.LevelWarn, "operation failed", 0)
				record.AddAttrs(slog.String("operation", "startup"))
				return record
			},
			expectedOutput: "operation failed: operation=startup",
			description:    "Should format message with single key=value attribute",
		},
		{
			name: "message with multiple attributes",
			setupRecord: func() slog.Record {
				record := slog.NewRecord(time.Now(), slog.LevelError, "user action", 0)
				record.AddAttrs(
					slog.String("user_id", "12345"),
					slog.String("action", "login"),
					slog.Int("attempt", 3),
				)
				return record
			},
			expectedOutput: "user action: user_id=12345 action=login attempt=3",
			description:    "Should format message with multiple attributes in key=value format",
		},
		{
			name: "message with nested group",
			setupRecord: func() slog.Record {
				record := slog.NewRecord(time.Now(), slog.LevelWarn, "request processed", 0)
				record.AddAttrs(
					slog.Group("http",
						slog.String("method", "GET"),
						slog.Int("status", 200),
					),
					slog.String("endpoint", "/api/users"),
				)
				return record
			},
			expectedOutput: "request processed: http.method=GET http.status=200 endpoint=/api/users",
			description:    "Should format nested groups with dot notation",
		},
		{
			name: "message with special characters in values",
			setupRecord: func() slog.Record {
				record := slog.NewRecord(time.Now(), slog.LevelError, "error occurred", 0)
				record.AddAttrs(
					slog.String("error", "connection failed: timeout after 30s"),
					slog.String("path", "/tmp/file with spaces.txt"),
				)
				return record
			},
			expectedOutput: `error occurred: error="connection failed: timeout after 30s" path="/tmp/file with spaces.txt"`,
			description:    "Should properly quote values with special characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := tt.setupRecord()
			result := backend.formatMessage(record)

			assert.Equal(t, tt.expectedOutput, result, tt.description)
		})
	}
}

func TestLoggerBackend_Add(t *testing.T) {
	backend := newLoggerBackend(2)

	t.Run("adds new log entry", func(t *testing.T) {
		record := slog.NewRecord(time.Now(), slog.LevelDebug, "test message", 0)
		backend.Add(record)

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.Equal(t, "test message", logs.Logs[0].Message)
		assert.Equal(t, LogDebug, logs.Logs[0].Level)
		assert.Equal(t, uint32(1), logs.Logs[0].Count)
	})

	t.Run("increments count for duplicate entries", func(t *testing.T) {
		backend := newLoggerBackend(10)
		record := slog.NewRecord(time.Now(), slog.LevelWarn, "duplicate message", 0)

		backend.Add(record)
		backend.Add(record)
		backend.Add(record)

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.Equal(t, "duplicate message", logs.Logs[0].Message)
		assert.Equal(t, uint32(3), logs.Logs[0].Count)
	})

	t.Run("respects max distinct logs limit", func(t *testing.T) {
		backend := newLoggerBackend(2)

		record1 := slog.NewRecord(time.Now(), slog.LevelDebug, "message 1", 0)
		record2 := slog.NewRecord(time.Now(), slog.LevelWarn, "message 2", 0)
		record3 := slog.NewRecord(time.Now(), slog.LevelError, "message 3", 0)

		backend.Add(record1)
		backend.Add(record2)
		backend.Add(record3) // Should be dropped due to limit

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		// Should have 2 regular messages + 1 warning about exceeding limit
		require.Len(t, logs.Logs, 3)

		// Check that the warning message is present
		foundWarning := false
		for _, log := range logs.Logs {
			if log.Message == "telemetry: log count exceeded maximum, dropping log" {
				foundWarning = true
				assert.Equal(t, LogError, log.Level)
				break
			}
		}
		assert.True(t, foundWarning, "Should contain warning message about exceeding log limit")
	})
}

func TestLoggerBackend_StackTrace(t *testing.T) {
	backend := newLoggerBackend(10)

	t.Run("captures stack trace when requested", func(t *testing.T) {
		record := slog.NewRecord(time.Now(), slog.LevelError, "error with stack", 123456) // Mock PC
		backend.Add(record, WithStacktrace())

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.NotEmpty(t, logs.Logs[0].StackTrace, "Should have stack trace when PC is present")
	})

	t.Run("no stack trace without PC", func(t *testing.T) {
		record := slog.NewRecord(time.Now(), slog.LevelError, "error without stack", 0)
		backend.Add(record, WithStacktrace())

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.Empty(t, logs.Logs[0].StackTrace, "Should have no stack trace when PC is 0")
	})
}

func TestLoggerBackend_Tags(t *testing.T) {
	backend := newLoggerBackend(10)

	t.Run("includes tags in log entry", func(t *testing.T) {
		record := slog.NewRecord(time.Now(), slog.LevelDebug, "tagged message", 0)
		backend.Add(record, WithTags([]string{"service:api", "version:1.2.3"}))

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.Equal(t, "service:api,version:1.2.3", logs.Logs[0].Tags)
	})
}

func TestUnwindStackFromPC(t *testing.T) {
	t.Run("returns empty string for zero PC", func(t *testing.T) {
		result := unwindStackFromPC(0)
		assert.Empty(t, result)
	})

	t.Run("returns stack trace for valid PC", func(t *testing.T) {
		// Get a valid PC from current call stack
		var pcs [1]uintptr
		n := runtime.Callers(1, pcs[:])
		require.Greater(t, n, 0, "Should capture at least one PC")

		result := unwindStackFromPC(pcs[0])
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "TestUnwindStackFromPC")

		// Should contain function name, file, and line number
		lines := strings.Split(result, "\n")
		assert.GreaterOrEqual(t, len(lines), 2, "Should have at least function and file:line")
	})
}
