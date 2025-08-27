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

	core, observedLogs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	logger.With(ExtractTracingContext(sctx)).Info("with tracing context")
	logger.With(ExtractTracingContext(context.Background())).Info("without tracing context")

	logs := observedLogs.All()
	assert.Len(t, logs, 2, "expected exactly two log entries")
	assert.Equal(t, "with tracing context", logs[0].Message)
	assert.Equal(t, "without tracing context", logs[1].Message)
	assert.Equal(t, zap.Skip(), logs[1].Context[0])

	ctxStr := getJSONStringLog(t, logs[0])
	assert.Contains(t, ctxStr, ext.LogKeyTraceID)
	assert.Contains(t, ctxStr, sp.Context().TraceID())
	assert.Contains(t, ctxStr, ext.LogKeySpanID)
	assert.Contains(t, ctxStr, "1234")

	noCtxStr := getJSONStringLog(t, logs[1])

	assert.NotContains(t, noCtxStr, ext.LogKeyTraceID)
	assert.NotContains(t, noCtxStr, ext.LogKeySpanID)
}

func getJSONStringLog(t *testing.T, entry observer.LoggedEntry) string {
	var buf bytes.Buffer
	encCore := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zapcore.DebugLevel)
	assert.NoError(t, encCore.Write(entry.Entry, entry.Context))
	return string(buf.String())
}
