// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"context"
	"strconv"
	"testing"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func fieldsToMap(fields []zap.Field) map[string]string {
	m := make(map[string]string, len(fields))
	for _, f := range fields {
		m[f.Key] = f.String
	}
	return m
}

func TestTraceFields(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))
	defer sp.Finish()

	fields := TraceFields(sctx)
	require.Len(t, fields, 2)

	data := fieldsToMap(fields)
	// By default, trace IDs are logged in 128-bit format.
	assert.Equal(t, sp.Context().TraceID(), data[ext.LogKeyTraceID])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data[ext.LogKeySpanID])
}

func TestTraceFields128BitDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")

	// Re-initialize to account for the race between setting the env var in the
	// test and reading it in the contrib.
	cfg = newConfig()

	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))
	defer sp.Finish()

	fields := TraceFields(sctx)
	require.Len(t, fields, 2)

	data := fieldsToMap(fields)
	assert.Equal(t, strconv.FormatUint(sp.Context().TraceIDLower(), 10), data[ext.LogKeyTraceID])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data[ext.LogKeySpanID])
}

func TestTraceFieldsNoSpan(t *testing.T) {
	assert.Nil(t, TraceFields(context.Background()))
}
