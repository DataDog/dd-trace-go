package zap

import (
	"bytes"
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestExtractTracingContext(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	t.Run("correlation and 128bit traceId enabled", func(t *testing.T) {
		// Create a new Zap logger with an observable core to capture the logs.
		core, observedLogs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)

		logger.With(ExtractTracingContext(sctx)).Info("with tracing context")
		logger.With(ExtractTracingContext(context.Background())).Info("without tracing context")

		logs := observedLogs.All()
		assert.Len(t, logs, 2, "expected exactly two log entries")

		// Verify first log entry has the tracing context
		assert.Equal(t, "with tracing context", logs[0].Message)
		assert.NotEqual(t, zap.Skip(), logs[0].Context[0])

		// JSON log should contain the tracing context properly formatted
		ctxStr := getJSONStringLog(t, logs[0])
		assert.Contains(t, ctxStr, `"`+ext.LogKeyTraceID+`":"`+sp.Context().TraceID()+`"`)
		assert.Contains(t, ctxStr, `"`+ext.LogKeySpanID+`":1234`)

		// Verify second log entry does not have the tracing context
		assert.Equal(t, "without tracing context", logs[1].Message)
		assert.Equal(t, zap.Skip(), logs[1].Context[0])

		// JSON log should not contain the tracing context.
		noCtxStr := getJSONStringLog(t, logs[1])
		assert.NotContains(t, noCtxStr, ext.LogKeyTraceID)
		assert.NotContains(t, noCtxStr, ext.LogKeySpanID)
	})

	t.Run("correlation enabled and 128bit traceId disabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
		cfg = newConfig() // reload the config

		// Create a new Zap logger with an observable core to capture the logs.
		core, observedLogs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)

		logger.With(ExtractTracingContext(sctx)).Info("with tracing context")

		logs := observedLogs.All()
		assert.Len(t, logs, 1, "expected exactly one log entry")

		// Verify log entry has the tracing context
		assert.Equal(t, "with tracing context", logs[0].Message)
		assert.NotEqual(t, zap.Skip(), logs[0].Context[0])

		// JSON log should contain the tracing context properly formatted
		ctxStr := getJSONStringLog(t, logs[0])
		assert.Contains(t, ctxStr, `"`+ext.LogKeyTraceID+`":"1234"`)
		assert.Contains(t, ctxStr, `"`+ext.LogKeySpanID+`":1234`)
	})

	t.Run("correlation and 128bit traceId disabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
		t.Setenv("DD_TRACE_ZAP_CORRELATION_INJECTION_ENABLED", "false")
		cfg = newConfig() // reload the config

		// Create a new Zap logger with an observable core to capture the logs.
		core, observedLogs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)

		logger.With(ExtractTracingContext(sctx)).Info("with tracing context")
		logger.With(ExtractTracingContext(context.Background())).Info("without tracing context")

		logs := observedLogs.All()
		assert.Len(t, logs, 2, "expected exactly two log entries")

		// Verify both log entries do not have the tracing context
		assert.Equal(t, "with tracing context", logs[0].Message)
		assert.Equal(t, zap.Skip(), logs[0].Context[0])

		assert.Equal(t, "without tracing context", logs[1].Message)
		assert.Equal(t, zap.Skip(), logs[1].Context[0])

		// JSON log should not contain the tracing context.
		ctxStr := getJSONStringLog(t, logs[0])
		assert.NotContains(t, ctxStr, ext.LogKeyTraceID)
		assert.NotContains(t, ctxStr, ext.LogKeySpanID)

		noCtxStr := getJSONStringLog(t, logs[1])
		assert.NotContains(t, noCtxStr, ext.LogKeyTraceID)
		assert.NotContains(t, noCtxStr, ext.LogKeySpanID)
	})
}

func getJSONStringLog(t *testing.T, entry observer.LoggedEntry) string {
	var buf bytes.Buffer
	encCore := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zapcore.DebugLevel)
	assert.NoError(t, encCore.Write(entry.Entry, entry.Context))
	return string(buf.String())
}
