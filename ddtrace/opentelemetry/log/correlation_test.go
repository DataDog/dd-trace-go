// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestDDSpanContextToOtel(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Start a DD span
	span := tracer.StartSpan("test")
	defer span.Finish()

	ddCtx := span.Context()

	// Convert to OTel span context
	otelCtx := ddSpanContextToOtel(ddCtx)

	// Verify it's valid
	assert.True(t, otelCtx.IsValid())

	// Verify trace ID matches
	expectedTraceID := ddCtx.TraceIDBytes()
	actualTraceID := otelCtx.TraceID()
	assert.Equal(t, expectedTraceID[:], actualTraceID[:])

	// Verify span ID matches
	spanIDBytes := make([]byte, 8)
	for i := 0; i < 8; i++ {
		spanIDBytes[i] = byte(ddCtx.SpanID() >> (56 - 8*i))
	}
	var expectedSpanID [8]byte
	copy(expectedSpanID[:], spanIDBytes)
	actualSpanID := otelCtx.SpanID()
	assert.Equal(t, expectedSpanID[:], actualSpanID[:])

	// Verify it's marked as sampled
	assert.True(t, otelCtx.IsSampled())
}

func TestContextWithDDSpan(t *testing.T) {
	t.Run("adds OTel span wrapper when only DD span present", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Start a DD span and add to context
		ddSpan := tracer.StartSpan("test")
		defer ddSpan.Finish()
		ctx := tracer.ContextWithSpan(context.Background(), ddSpan)

		// Verify DD span is in context
		retrievedDD, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, ddSpan, retrievedDD)

		// Verify OTel span is NOT in context initially
		otelSpan := oteltrace.SpanFromContext(ctx)
		assert.False(t, otelSpan.SpanContext().IsValid())

		// Bridge the DD span
		bridgedCtx := contextWithDDSpan(ctx)

		// Now OTel span should be present
		otelSpan = oteltrace.SpanFromContext(bridgedCtx)
		assert.True(t, otelSpan.SpanContext().IsValid())

		// Verify IDs match
		expectedTID := ddSpan.Context().TraceIDBytes()
		actualTID := otelSpan.SpanContext().TraceID()
		assert.Equal(t, expectedTID[:], actualTID[:])
	})

	t.Run("preserves existing OTel span", func(t *testing.T) {
		// Create OTel tracer
		provider := opentelemetry.NewTracerProvider()
		otelTracer := provider.Tracer("test")

		// Start OTel span
		ctx, otelSpan := otelTracer.Start(context.Background(), "test")
		defer otelSpan.End()

		originalSpanCtx := otelSpan.SpanContext()

		// Bridge should preserve the OTel span
		bridgedCtx := contextWithDDSpan(ctx)

		retrievedSpan := oteltrace.SpanFromContext(bridgedCtx)
		retrievedSpanCtx := retrievedSpan.SpanContext()

		// Compare the important fields
		assert.Equal(t, originalSpanCtx.TraceID(), retrievedSpanCtx.TraceID())
		assert.Equal(t, originalSpanCtx.SpanID(), retrievedSpanCtx.SpanID())
		assert.Equal(t, originalSpanCtx.TraceFlags(), retrievedSpanCtx.TraceFlags())
		assert.Equal(t, originalSpanCtx.IsRemote(), retrievedSpanCtx.IsRemote())

		// For TraceState, check individual components rather than exact string equality
		// because iterating through propagatingTags map doesn't guarantee order in tracestate
		originalState := originalSpanCtx.TraceState().String()
		retrievedState := retrievedSpanCtx.TraceState().String()

		// Both should have the dd vendor
		assert.Contains(t, originalState, "dd=")
		assert.Contains(t, retrievedState, "dd=")

		// Extract just the dd vendor part (before any comma)
		originalDD := strings.SplitN(originalState, ",", 2)[0]
		retrievedDD := strings.SplitN(retrievedState, ",", 2)[0]

		// Check that both contain the same fields (order-independent)
		// Extract expected fields from original
		for _, field := range []string{"s:", "p:", "t.tid:", "t.dm:"} {
			if strings.Contains(originalDD, field) {
				assert.Contains(t, retrievedDD, field, "retrieved tracestate should contain field %s", field)
			}
		}
	})

	t.Run("returns original context when no spans present", func(t *testing.T) {
		ctx := context.Background()
		bridgedCtx := contextWithDDSpan(ctx)
		assert.Equal(t, ctx, bridgedCtx)
	})
}

func TestLogCorrelation(t *testing.T) {
	t.Run("DD span IDs appear in exported logs", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Create a test exporter to capture logs
		exporter := newTestExporter()

		// Create LoggerProvider with test exporter
		resource, err := buildResource(context.Background())
		require.NoError(t, err)

		processor := sdklog.NewSimpleProcessor(exporter)
		provider := sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)
		defer func() {
			_ = provider.Shutdown(context.Background())
		}()

		// Wrap with DD-aware provider
		ddProvider := &ddAwareLoggerProvider{underlying: provider}

		// Get a logger (DD-aware)
		logger := ddProvider.Logger("test")

		// Start a DD span
		ddSpan := tracer.StartSpan("test-operation")
		defer ddSpan.Finish()

		ddCtx := ddSpan.Context()
		ctx := tracer.ContextWithSpan(context.Background(), ddSpan)

		// NO explicit bridge needed - the DD-aware logger handles it automatically

		// Emit a log with the context
		var logRecord log.Record
		logRecord.SetBody(log.StringValue("test log message"))
		logRecord.SetSeverity(log.SeverityInfo)

		logger.Emit(ctx, logRecord)

		// Force flush
		err = provider.ForceFlush(context.Background())
		require.NoError(t, err)

		// Get exported records
		records := exporter.GetRecords()
		require.Len(t, records, 1)

		exportedRecord := records[0]

		// Verify trace ID matches
		expectedTraceID := ddCtx.TraceIDBytes()
		actualTraceID := exportedRecord.TraceID()
		assert.Equal(t, expectedTraceID[:], actualTraceID[:])

		// Verify span ID matches
		spanIDBytes := make([]byte, 8)
		for i := 0; i < 8; i++ {
			spanIDBytes[i] = byte(ddCtx.SpanID() >> (56 - 8*i))
		}
		var expectedSpanID [8]byte
		copy(expectedSpanID[:], spanIDBytes)
		actualSpanID := exportedRecord.SpanID()
		assert.Equal(t, expectedSpanID[:], actualSpanID[:])

		// Verify trace flags (sampled)
		assert.Equal(t, byte(oteltrace.FlagsSampled), byte(exportedRecord.TraceFlags()))
	})

	t.Run("OTel span IDs appear in exported logs", func(t *testing.T) {
		// Create OTel tracer
		provider := opentelemetry.NewTracerProvider()
		otelTracer := provider.Tracer("test")

		// Create a test exporter to capture logs
		exporter := newTestExporter()

		// Create LoggerProvider with test exporter
		resource, err := buildResource(context.Background())
		require.NoError(t, err)

		processor := sdklog.NewSimpleProcessor(exporter)
		logProvider := sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)
		defer func() {
			_ = logProvider.Shutdown(context.Background())
		}()

		// Wrap with DD-aware provider
		ddProvider := &ddAwareLoggerProvider{underlying: logProvider}

		// Get a logger (DD-aware)
		logger := ddProvider.Logger("test")

		// Start an OTel span
		ctx, otelSpan := otelTracer.Start(context.Background(), "test-operation")
		defer otelSpan.End()

		otelSpanCtx := otelSpan.SpanContext()

		// Emit a log with the context
		var logRecord log.Record
		logRecord.SetBody(log.StringValue("test log message from otel span"))
		logRecord.SetSeverity(log.SeverityInfo)

		logger.Emit(ctx, logRecord)

		// Force flush
		err = logProvider.ForceFlush(context.Background())
		require.NoError(t, err)

		// Get exported records
		records := exporter.GetRecords()
		require.Len(t, records, 1)

		exportedRecord := records[0]

		// Verify trace ID matches
		expectedTID := otelSpanCtx.TraceID()
		actualTID := exportedRecord.TraceID()
		assert.Equal(t, expectedTID[:], actualTID[:])

		// Verify span ID matches
		expectedSID := otelSpanCtx.SpanID()
		actualSID := exportedRecord.SpanID()
		assert.Equal(t, expectedSID[:], actualSID[:])

		// Verify trace flags
		assert.Equal(t, byte(otelSpanCtx.TraceFlags()), byte(exportedRecord.TraceFlags()))
	})

	t.Run("logs without span have no trace context", func(t *testing.T) {
		// Create a test exporter to capture logs
		exporter := newTestExporter()

		// Create LoggerProvider with test exporter
		resource, err := buildResource(context.Background())
		require.NoError(t, err)

		processor := sdklog.NewSimpleProcessor(exporter)
		provider := sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)
		defer func() {
			_ = provider.Shutdown(context.Background())
		}()

		// Wrap with DD-aware provider
		ddProvider := &ddAwareLoggerProvider{underlying: provider}

		// Get a logger (DD-aware)
		logger := ddProvider.Logger("test")

		// Emit a log WITHOUT any span context
		ctx := context.Background()
		var logRecord log.Record
		logRecord.SetBody(log.StringValue("test log without span"))
		logRecord.SetSeverity(log.SeverityInfo)

		logger.Emit(ctx, logRecord)

		// Force flush
		err = provider.ForceFlush(context.Background())
		require.NoError(t, err)

		// Get exported records
		records := exporter.GetRecords()
		require.Len(t, records, 1)

		exportedRecord := records[0]

		// Verify no trace ID
		assert.False(t, exportedRecord.TraceID().IsValid())

		// Verify no span ID
		assert.False(t, exportedRecord.SpanID().IsValid())
	})

	t.Run("mixed DD and OTel spans in trace hierarchy", func(t *testing.T) {
		// Use real tracer to get valid span IDs
		tracer.Start()
		defer tracer.Stop()

		// Create OTel tracer
		otelProvider := opentelemetry.NewTracerProvider()
		otelTracer := otelProvider.Tracer("test")

		// Create a test exporter to capture logs
		exporter := newTestExporter()

		// Create LoggerProvider with test exporter
		resource, err := buildResource(context.Background())
		require.NoError(t, err)

		processor := sdklog.NewSimpleProcessor(exporter)
		logProvider := sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)
		defer func() {
			_ = logProvider.Shutdown(context.Background())
		}()

		// Wrap with DD-aware provider
		ddProvider := &ddAwareLoggerProvider{underlying: logProvider}

		// Get a logger (DD-aware - automatic bridging)
		logger := ddProvider.Logger("test")

		// Start DD span
		ddSpan := tracer.StartSpan("dd-parent")
		defer ddSpan.Finish()
		ctx := tracer.ContextWithSpan(context.Background(), ddSpan)

		// Log from DD span context (automatic bridging by DD-aware logger)
		var logRecord1 log.Record
		logRecord1.SetBody(log.StringValue("log from DD span"))
		logRecord1.SetSeverity(log.SeverityInfo)
		logger.Emit(ctx, logRecord1)

		// Start OTel child span
		ctx2, otelSpan := otelTracer.Start(ctx, "otel-child")
		defer otelSpan.End()

		// Log from OTel span context
		var logRecord2 log.Record
		logRecord2.SetBody(log.StringValue("log from OTel span"))
		logRecord2.SetSeverity(log.SeverityInfo)
		logger.Emit(ctx2, logRecord2)

		// Force flush
		err = logProvider.ForceFlush(context.Background())
		require.NoError(t, err)

		// Get exported records
		records := exporter.GetRecords()
		require.Len(t, records, 2)

		// Both should have valid trace IDs
		assert.True(t, records[0].TraceID().IsValid())
		assert.True(t, records[1].TraceID().IsValid())

		// Both should have valid span IDs
		assert.True(t, records[0].SpanID().IsValid())
		assert.True(t, records[1].SpanID().IsValid())

		// First log should have DD span ID
		ddSpanIDBytes := make([]byte, 8)
		for i := 0; i < 8; i++ {
			ddSpanIDBytes[i] = byte(ddSpan.Context().SpanID() >> (56 - 8*i))
		}
		var expectedDDSpanID [8]byte
		copy(expectedDDSpanID[:], ddSpanIDBytes)
		actualSID1 := records[0].SpanID()
		assert.Equal(t, expectedDDSpanID[:], actualSID1[:])

		// Second log should have OTel span ID
		expectedOtelSID := otelSpan.SpanContext().SpanID()
		actualSID2 := records[1].SpanID()
		assert.Equal(t, expectedOtelSID[:], actualSID2[:])
	})
}
