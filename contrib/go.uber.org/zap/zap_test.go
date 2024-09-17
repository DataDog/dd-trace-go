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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	internallog "gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func TestWithTraceFields(t *testing.T) {
	tracer.Start(tracer.WithLogger(internallog.DiscardLogger{}))
	defer tracer.Stop()

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	observed, logs := observer.New(zapcore.InfoLevel)

	logger := zap.New(observed)
	logger = WithTraceFields(ctx, logger)
	logger.Info("some message")

	traceID := strconv.FormatUint(span.Context().TraceID(), 10)
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)

	require.Equal(t, 1, logs.Len())
	infoLog := logs.All()[0]

	require.Len(t, infoLog.Context, 2)
	assert.Equal(t, "dd.trace_id", infoLog.Context[0].Key)
	assert.Equal(t, traceID, infoLog.Context[0].String)
	assert.Equal(t, "dd.span_id", infoLog.Context[1].Key)
	assert.Equal(t, spanID, infoLog.Context[1].String)
}
