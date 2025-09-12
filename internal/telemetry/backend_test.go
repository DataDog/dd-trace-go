// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

func TestLoggerBackend_formatMessage(t *testing.T) {
	backend := newLoggerBackend(100)

	tests := []struct {
		name           string
		setupRecord    func() Record
		expectedOutput string
		description    string
	}{
		{
			name: "plain message without attributes",
			setupRecord: func() Record {
				return NewRecord(LogDebug, "simple message")
			},
			expectedOutput: "simple message",
			description:    "Should return raw message when no attributes present",
		},
		{
			name: "message with single attribute",
			setupRecord: func() Record {
				record := NewRecord(LogWarn, "operation failed")
				record.AddAttrs(slog.String("operation", "startup"))
				return record
			},
			expectedOutput: "operation failed: operation=startup",
			description:    "Should format message with single key=value attribute",
		},
		{
			name: "message with multiple attributes",
			setupRecord: func() Record {
				record := NewRecord(LogError, "user action")
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
			setupRecord: func() Record {
				record := NewRecord(LogWarn, "request processed")
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
			setupRecord: func() Record {
				record := NewRecord(LogError, "error occurred")
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
		record := NewRecord(LogDebug, "test message")
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
		record := NewRecord(LogWarn, "duplicate message")

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

		record1 := NewRecord(LogDebug, "message 1")
		record2 := NewRecord(LogWarn, "message 2")
		record3 := NewRecord(LogError, "message 3")

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
		record := NewRecord(LogError, "error with stack")
		backend.Add(record, WithStacktrace())

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.NotEmpty(t, logs.Logs[0].StackTrace, "Should have stack trace when PC is present")
	})

	t.Run("captures stack trace with redaction", func(t *testing.T) {
		record := NewRecord(LogError, "error with stack trace")
		backend.Add(record, WithStacktrace())

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.NotEmpty(t, logs.Logs[0].StackTrace, "Should have stack trace when using CaptureWithRedaction")
		assert.Contains(t, logs.Logs[0].StackTrace, "backend_test.go", "Stack trace should show test location")
	})

	t.Run("telemetry skip value correctly excludes internal frames", func(t *testing.T) {
		// This test verifies that telemetryStackSkip=4 correctly skips the internal telemetry frames
		// Call chain: this test -> backend.Add -> backend.add -> CaptureWithRedaction -> capture
		record := NewRecord(LogError, "test telemetry skip")
		backend.Add(record, WithStacktrace())

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		stackTrace := logs.Logs[0].StackTrace
		assert.NotEmpty(t, stackTrace, "Should have stack trace")

		// With skip=4, the stack should NOT contain backend.add, backend.Add, CaptureWithRedaction, or capture
		assert.NotContains(t, stackTrace, "backend.add", "Should skip backend.add frame")
		assert.NotContains(t, stackTrace, "backend.Add", "Should skip backend.Add frame")
		assert.NotContains(t, stackTrace, "CaptureWithRedaction", "Should skip CaptureWithRedaction frame")
		assert.NotContains(t, stackTrace, ".capture", "Should skip capture frame")

		// Should contain this test function
		assert.Contains(t, stackTrace, "TestLoggerBackend_StackTrace", "Should contain calling test function")
		assert.Contains(t, stackTrace, "backend_test.go", "Should show test file location")
	})
}

func TestLoggerBackend_Tags(t *testing.T) {
	backend := newLoggerBackend(10)

	t.Run("includes tags in log entry", func(t *testing.T) {
		record := NewRecord(LogDebug, "tagged message")
		backend.Add(record, WithTags([]string{"service:api", "version:1.2.3"}))

		payload := backend.Payload()
		require.NotNil(t, payload)

		logs := payload.(transport.Logs)
		require.Len(t, logs.Logs, 1)

		assert.Equal(t, "service:api,version:1.2.3", logs.Logs[0].Tags)
	})
}
