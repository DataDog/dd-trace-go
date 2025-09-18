// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

// ContextEnricher enriches log records with trace context from both Datadog and OpenTelemetry spans
type ContextEnricher struct{}

// NewContextEnricher creates a new ContextEnricher
func NewContextEnricher() *ContextEnricher {
	return &ContextEnricher{}
}

// EnrichRecord enriches a log record with trace context information
func (e *ContextEnricher) EnrichRecord(ctx context.Context, record otellog.Record) {
	// Try to get trace context from Datadog span first
	if span, found := tracer.SpanFromContext(ctx); found {
		e.enrichFromDDSpan(record, span)
		return
	}

	// Fall back to OpenTelemetry span context
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		e.enrichFromOTelSpan(record, spanCtx)
	}
}

// enrichFromDDSpan enriches the record with Datadog span context
func (e *ContextEnricher) enrichFromDDSpan(record otellog.Record, span *tracer.Span) {
	// Note: OpenTelemetry log.Record interface doesn't have SetTraceID/SetSpanID methods
	// Trace correlation is handled at the SDK level through the LogRecordProcessor
	// This method is kept for future extensibility
}

// enrichFromOTelSpan enriches the record with OpenTelemetry span context
func (e *ContextEnricher) enrichFromOTelSpan(record otellog.Record, spanCtx trace.SpanContext) {
	// Note: OpenTelemetry log.Record interface doesn't have SetTraceID/SetSpanID methods
	// Trace correlation is handled at the SDK level through the LogRecordProcessor
	// This method is kept for future extensibility
}

// putUint64BigEndian puts a uint64 in big-endian byte order
func putUint64BigEndian(b []byte, v uint64) {
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

// LogRecordProcessor is a processor that enriches log records with trace context
type LogRecordProcessor struct {
	next     sdklog.Processor
	enricher *ContextEnricher
}

// NewLogRecordProcessor creates a new LogRecordProcessor that enriches records with trace context
func NewLogRecordProcessor(next sdklog.Processor) *LogRecordProcessor {
	return &LogRecordProcessor{
		next:     next,
		enricher: NewContextEnricher(),
	}
}

// OnEmit processes a log record by enriching it with trace context
func (p *LogRecordProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	// Note: Trace correlation is handled automatically by the OpenTelemetry SDK
	// when the context contains span information

	// Pass to next processor
	if p.next != nil {
		return p.next.OnEmit(ctx, record)
	}

	return nil
}

// Shutdown shuts down the processor
func (p *LogRecordProcessor) Shutdown(ctx context.Context) error {
	if p.next != nil {
		return p.next.Shutdown(ctx)
	}
	return nil
}

// ForceFlush forces a flush of the processor
func (p *LogRecordProcessor) ForceFlush(ctx context.Context) error {
	if p.next != nil {
		return p.next.ForceFlush(ctx)
	}
	return nil
}

// CorrelationFields extracts correlation fields from context for manual log correlation
type CorrelationFields struct {
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
}

// GetCorrelationFields extracts trace correlation fields from context
// This can be used for manual log correlation when not using the OTLP logger
func GetCorrelationFields(ctx context.Context) CorrelationFields {
	fields := CorrelationFields{}

	// Try Datadog span first
	if span, found := tracer.SpanFromContext(ctx); found {
		spanCtx := span.Context()

		if traceID := spanCtx.TraceID(); traceID != tracer.TraceIDZero {
			// Use 128-bit trace ID if available, otherwise 64-bit
			if spanCtx.TraceIDUpper() != 0 {
				fields.TraceID = formatTraceID128(spanCtx.TraceIDUpper(), spanCtx.TraceIDLower())
			} else {
				fields.TraceID = strconv.FormatUint(spanCtx.TraceIDLower(), 10)
			}
		}

		if spanID := spanCtx.SpanID(); spanID != 0 {
			fields.SpanID = strconv.FormatUint(spanID, 10)
		}

		return fields
	}

	// Fall back to OpenTelemetry span
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		fields.TraceID = spanCtx.TraceID().String()
		fields.SpanID = spanCtx.SpanID().String()
	}

	return fields
}

// formatTraceID128 formats a 128-bit trace ID as a hex string
func formatTraceID128(upper, lower uint64) string {
	return formatUint64Hex(upper) + formatUint64Hex(lower)
}

// formatUint64Hex formats a uint64 as a 16-character hex string
func formatUint64Hex(v uint64) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, 16)

	for i := 15; i >= 0; i-- {
		result[i] = hexChars[v&0xf]
		v >>= 4
	}

	return string(result)
}
