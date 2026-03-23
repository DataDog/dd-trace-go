// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

var _ traceWriter = (*otlpTraceWriter)(nil)

// otlpTraceWriter is a no-op placeholder for the OTLP export trace writer.
// TODO: implement OTLP protobuf encoding and export to OTel Collector.
type otlpTraceWriter struct{}

func newOTLPTraceWriter(_ *config) *otlpTraceWriter {
	return &otlpTraceWriter{}
}

func (w *otlpTraceWriter) add(_ []*Span) {}

func (w *otlpTraceWriter) flush() {}

func (w *otlpTraceWriter) stop() {}
