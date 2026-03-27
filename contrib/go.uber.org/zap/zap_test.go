// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestTraceFields(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	logger.Info("test", TraceFields(sctx)...)

	var data map[string]interface{}
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &data))
	assert.Equal(t, sp.Context().TraceID(), data["dd.trace_id"])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data["dd.span_id"])
}

func TestTraceFields128BitDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	cfg = newConfig()

	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	logger.Info("test", TraceFields(sctx)...)

	var data map[string]interface{}
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &data))
	assert.Equal(t, strconv.FormatUint(sp.Context().TraceIDLower(), 10), data["dd.trace_id"])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data["dd.span_id"])
}

func TestTraceFieldsNoSpan(t *testing.T) {
	fields := TraceFields(context.Background())
	assert.Nil(t, fields)
}

func newTestLogger(buf *bytes.Buffer) *zap.Logger {
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(buf), zapcore.DebugLevel)
	return zap.New(core)
}
