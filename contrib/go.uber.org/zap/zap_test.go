// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"context"
	"io"
	"strconv"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

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
	// test and reading it in the contrib. Restore the original config afterwards
	// so this override doesn't leak into other tests or benchmarks.
	orig := cfg
	t.Cleanup(func() { cfg = orig })
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

// newBenchLogger returns a zap.Logger that discards its output, so benchmarks
// measure only the trace-field injection overhead, not encoding or I/O.
func newBenchLogger() *zap.Logger {
	return zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(io.Discard),
		zapcore.DebugLevel,
	))
}

// BenchmarkTraceFields measures the cost of extracting the trace and span IDs
// from a context into zap.Field values.
func BenchmarkTraceFields(b *testing.B) {
	tracer.Start()
	defer tracer.Stop()
	sp, ctx := tracer.StartSpanFromContext(context.Background(), "bench")
	defer sp.Finish()

	b.ReportAllocs()
	for b.Loop() {
		_ = TraceFields(ctx)
	}
}

// BenchmarkLogger compares a plain log call against the form Orchestrion
// injects, logger.With(TraceFields(ctx)...).Info(...), so the delta between the
// two sub-benchmarks is the per-call instrumentation overhead.
func BenchmarkLogger(b *testing.B) {
	tracer.Start()
	defer tracer.Stop()
	sp, ctx := tracer.StartSpanFromContext(context.Background(), "bench")
	defer sp.Finish()

	logger := newBenchLogger()

	b.Run("baseline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.Info("bench message")
		}
	})
	b.Run("instrumented", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.With(TraceFields(ctx)...).Info("bench message")
		}
	})
}

// BenchmarkSugaredLogger mirrors BenchmarkLogger for the SugaredLogger path,
// which Orchestrion instruments via Desugar().With(...).Sugar().
func BenchmarkSugaredLogger(b *testing.B) {
	tracer.Start()
	defer tracer.Stop()
	sp, ctx := tracer.StartSpanFromContext(context.Background(), "bench")
	defer sp.Finish()

	sugar := newBenchLogger().Sugar()

	b.Run("baseline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sugar.Infow("bench message")
		}
	})
	b.Run("instrumented", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sugar.Desugar().With(TraceFields(ctx)...).Sugar().Infow("bench message")
		}
	})
}
