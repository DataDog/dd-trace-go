// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestWithTraceFields(t *testing.T) {
	tracer.Start(
		tracer.WithLogger(testutils.DiscardLogger()),
	)
	defer tracer.Stop()

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	observed, logs := observer.New(zapcore.InfoLevel)

	logger := zap.New(observed)
	logger = WithTraceFields(ctx, logger)
	logger.Info("some message")

	// By default, 128-bit trace IDs are enabled
	traceID := span.Context().TraceID()
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)

	require.Equal(t, 1, logs.Len())
	infoLog := logs.All()[0]

	require.Len(t, infoLog.Context, 2)
	assert.Equal(t, "dd.trace_id", infoLog.Context[0].Key)
	assert.Equal(t, traceID, infoLog.Context[0].String)
	assert.Equal(t, "dd.span_id", infoLog.Context[1].Key)
	assert.Equal(t, spanID, infoLog.Context[1].String)
}

func TestWithTraceFields128BitDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")

	// Re-initialize to account for race condition between setting env var in the test and reading it in the contrib
	cfg = newConfig()

	tracer.Start(
		tracer.WithLogger(testutils.DiscardLogger()),
	)
	defer tracer.Stop()

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	observed, logs := observer.New(zapcore.InfoLevel)

	logger := zap.New(observed)
	logger = WithTraceFields(ctx, logger)
	logger.Info("some message")

	// With 128-bit disabled, should use 64-bit trace ID
	traceID := strconv.FormatUint(span.Context().TraceIDLower(), 10)
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)

	require.Equal(t, 1, logs.Len())
	infoLog := logs.All()[0]

	require.Len(t, infoLog.Context, 2)
	assert.Equal(t, "dd.trace_id", infoLog.Context[0].Key)
	assert.Equal(t, traceID, infoLog.Context[0].String)
	assert.Equal(t, "dd.span_id", infoLog.Context[1].Key)
	assert.Equal(t, spanID, infoLog.Context[1].String)
}
