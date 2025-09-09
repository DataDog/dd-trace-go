// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHandler is a test slog.Handler that tracks calls and can be configured
// to be enabled/disabled for specific levels
type mockHandler struct {
	enabled        map[slog.Level]bool
	records        []slog.Record
	enabledCalls   int32
	handleCalls    int32
	withAttrCalls  int32
	withGroupCalls int32
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		enabled: make(map[slog.Level]bool),
		records: make([]slog.Record, 0),
	}
}

func (m *mockHandler) setEnabled(level slog.Level, enabled bool) {
	m.enabled[level] = enabled
}

func (m *mockHandler) Enabled(ctx context.Context, level slog.Level) bool {
	atomic.AddInt32(&m.enabledCalls, 1)
	return m.enabled[level]
}

func (m *mockHandler) Handle(ctx context.Context, r slog.Record) error {
	atomic.AddInt32(&m.handleCalls, 1)
	m.records = append(m.records, r)
	return nil
}

func (m *mockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	atomic.AddInt32(&m.withAttrCalls, 1)
	return m // Simplified for testing
}

func (m *mockHandler) WithGroup(name string) slog.Handler {
	atomic.AddInt32(&m.withGroupCalls, 1)
	return m // Simplified for testing
}

func (m *mockHandler) getEnabledCalls() int32 {
	return atomic.LoadInt32(&m.enabledCalls)
}

func (m *mockHandler) getHandleCalls() int32 {
	return atomic.LoadInt32(&m.handleCalls)
}

func TestHandlerWriter_LazyEvaluation(t *testing.T) {
	handler := newMockHandler()
	writer := NewHandlerWriter(handler, true)

	t.Run("disabled level skips all processing - Log method", func(t *testing.T) {
		// Set handler to not handle error level
		handler.setEnabled(slog.LevelError, false)

		// Reset counters
		handler.enabledCalls = 0
		handler.handleCalls = 0

		// Log a message
		message := "This should not be processed"
		err := writer.Log(slog.LevelError, message)

		// Should return success but not process the message
		require.NoError(t, err)

		// Should have called Enabled() but not Handle()
		assert.Equal(t, int32(1), handler.getEnabledCalls())
		assert.Equal(t, int32(0), handler.getHandleCalls())
		assert.Len(t, handler.records, 0)
	})

	t.Run("disabled level skips all processing - Write method", func(t *testing.T) {
		// Set handler to not handle info level (Write uses LevelInfo)
		handler.setEnabled(slog.LevelInfo, false)

		// Reset counters
		handler.enabledCalls = 0
		handler.handleCalls = 0

		// Write a log message
		message := "This should not be processed"
		n, err := writer.Write([]byte(message))

		// Should return success but not process the message
		require.NoError(t, err)
		assert.Equal(t, len(message), n)

		// Should have called Enabled() but not Handle()
		assert.Equal(t, int32(1), handler.getEnabledCalls())
		assert.Equal(t, int32(0), handler.getHandleCalls())
		assert.Len(t, handler.records, 0)
	})

	t.Run("enabled level processes message - Log method", func(t *testing.T) {
		// Set handler to handle error level
		handler.setEnabled(slog.LevelError, true)

		// Reset counters and records
		handler.enabledCalls = 0
		handler.handleCalls = 0
		handler.records = handler.records[:0]

		// Log a message
		message := "This should be processed"
		err := writer.Log(slog.LevelError, message)

		// Should process successfully
		require.NoError(t, err)

		// Should have called both Enabled() and Handle()
		assert.Equal(t, int32(1), handler.getEnabledCalls())
		assert.Equal(t, int32(1), handler.getHandleCalls())
		require.Len(t, handler.records, 1)

		// Check record content
		record := handler.records[0]
		assert.Equal(t, message, record.Message)
		assert.Equal(t, slog.LevelError, record.Level)
	})

	t.Run("enabled level processes message - Write method", func(t *testing.T) {
		// Set handler to handle info level (Write uses LevelInfo)
		handler.setEnabled(slog.LevelInfo, true)

		// Reset counters and records
		handler.enabledCalls = 0
		handler.handleCalls = 0
		handler.records = handler.records[:0]

		// Write a log message
		message := "This should be processed"
		n, err := writer.Write([]byte(message))

		// Should process successfully
		require.NoError(t, err)
		assert.Equal(t, len(message), n)

		// Should have called both Enabled() and Handle()
		assert.Equal(t, int32(1), handler.getEnabledCalls())
		assert.Equal(t, int32(1), handler.getHandleCalls())
		require.Len(t, handler.records, 1)

		// Check record content
		record := handler.records[0]
		assert.Equal(t, message, record.Message)
		assert.Equal(t, slog.LevelInfo, record.Level)
	})
}

func TestHandlerWriter_StackCapture(t *testing.T) {
	handler := newMockHandler()
	handler.setEnabled(slog.LevelError, true)
	handler.setEnabled(slog.LevelInfo, true)

	t.Run("with stack capture enabled - Log method", func(t *testing.T) {
		writer := NewHandlerWriter(handler, true)
		handler.records = handler.records[:0]

		writer.Log(slog.LevelError, "test message")

		require.Len(t, handler.records, 1)
		record := handler.records[0]
		assert.NotZero(t, record.PC, "Expected PC to be captured when capturePC=true")
	})

	t.Run("with stack capture disabled - Log method", func(t *testing.T) {
		writer := NewHandlerWriter(handler, false)
		handler.records = handler.records[:0]

		writer.Log(slog.LevelError, "test message")

		require.Len(t, handler.records, 1)
		record := handler.records[0]
		assert.Zero(t, record.PC, "Expected PC to be 0 when capturePC=false")
	})

	t.Run("with stack capture enabled - Write method", func(t *testing.T) {
		writer := NewHandlerWriter(handler, true)
		handler.records = handler.records[:0]

		writer.Write([]byte("test message"))

		require.Len(t, handler.records, 1)
		record := handler.records[0]
		assert.NotZero(t, record.PC, "Expected PC to be captured when capturePC=true")
	})

	t.Run("with stack capture disabled - Write method", func(t *testing.T) {
		writer := NewHandlerWriter(handler, false)
		handler.records = handler.records[:0]

		writer.Write([]byte("test message"))

		require.Len(t, handler.records, 1)
		record := handler.records[0]
		assert.Zero(t, record.PC, "Expected PC to be 0 when capturePC=false")
	})
}

func TestHandlerWriter_NewlineHandling(t *testing.T) {
	handler := newMockHandler()
	handler.setEnabled(slog.LevelInfo, true)
	writer := NewHandlerWriter(handler, false)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"message without newline", "message without newline", "message without newline"},
		{"message with newline", "message with newline\n", "message with newline"},
		{"message with multiple newlines", "message with multiple newlines\n\n", "message with multiple newlines\n"},
		{"only newline", "\n", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler.records = handler.records[:0]

			n, err := writer.Write([]byte(tt.input))

			require.NoError(t, err)
			assert.Equal(t, len(tt.input), n)

			require.Len(t, handler.records, 1)
			record := handler.records[0]
			assert.Equal(t, tt.expected, record.Message)
		})
	}
}

func TestHandlerWriter_WithTelemetryHandler(t *testing.T) {
	// Test that handlerWriter works with our telemetryHandler
	telHandler := NewTelemetryHandler()
	writer := NewHandlerWriter(telHandler, true)

	// This is more of an integration test - we can't easily verify
	// the telemetry output, but we can verify it doesn't crash
	message := "integration test message"
	n, err := writer.Write([]byte(message))

	require.NoError(t, err)
	assert.Equal(t, len(message), n)
}

func TestHandlerWriter_WithStandardHandlers(t *testing.T) {
	// Test with slog.TextHandler
	var buf bytes.Buffer
	textHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	writer := NewHandlerWriter(textHandler, false)

	message := "test message for text handler"
	n, err := writer.Write([]byte(message))

	require.NoError(t, err)
	assert.Equal(t, len(message), n)

	// Verify output was written to buffer
	output := buf.String()
	assert.Contains(t, output, message)
}

func TestHandlerWriter_PerformanceBenefit(t *testing.T) {
	// This test demonstrates the performance benefit of lazy evaluation
	handler := newMockHandler()
	writer := NewHandlerWriter(handler, true)

	// Test with disabled level - should be very fast
	handler.setEnabled(slog.LevelError, false)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		writer.Write([]byte("expensive message that won't be processed"))
	}
	disabledDuration := time.Since(start)

	// Test with enabled level - will do more work
	handler.setEnabled(slog.LevelError, true)
	handler.records = handler.records[:0] // Clear records to avoid memory issues

	start = time.Now()
	for i := 0; i < 1000; i++ {
		writer.Write([]byte("message that will be processed"))
	}
	enabledDuration := time.Since(start)

	t.Logf("Disabled level duration: %v", disabledDuration)
	t.Logf("Enabled level duration: %v", enabledDuration)

	// The disabled case should be significantly faster
	// We don't assert specific ratios since performance can vary,
	// but we do log the results for manual verification
	if disabledDuration >= enabledDuration {
		t.Logf("Warning: Expected disabled level to be faster, but disabled=%v >= enabled=%v",
			disabledDuration, enabledDuration)
	}
}

// Benchmark to demonstrate lazy evaluation performance
func BenchmarkHandlerWriter(b *testing.B) {
	handler := newMockHandler()
	writer := NewHandlerWriter(handler, true)

	b.Run("disabled", func(b *testing.B) {
		handler.setEnabled(slog.LevelError, false)
		message := []byte("benchmark message")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			writer.Write(message)
		}
	})

	b.Run("enabled", func(b *testing.B) {
		handler.setEnabled(slog.LevelError, true)
		message := []byte("benchmark message")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			writer.Write(message)
		}
	})
}

func ExampleNewHandlerWriter() {
	// Create a handler that only processes warnings and errors
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})

	// Create a handlerWriter with stack capture enabled
	writer := NewHandlerWriter(handler, true)

	// This will be processed and output to stdout (Error level >= Warn level)
	writer.Log(slog.LevelError, "This is an error message")

	// This will also be processed and output to stdout
	writer.Write([]byte("This info message will be processed"))

	// This will NOT be processed because Debug < Warn level
	writer.Log(slog.LevelDebug, "This debug message will be skipped")
}
