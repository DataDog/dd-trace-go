// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"context"
	"time"
)

// Tracer represents the interface for the underlying APM tracer.
type Tracer interface {
	// StartSpan starts a new APM span with the given name and configuration.
	StartSpan(ctx context.Context, name string, cfg StartAPMSpanConfig) (APMSpan, context.Context)
}

// StartAPMSpanConfig contains configuration options for starting an APM span.
type StartAPMSpanConfig struct {
	// SpanType is the type of the APM span.
	SpanType string
	// StartTime is the start time for the span.
	StartTime time.Time
}

// FinishAPMSpanConfig contains configuration options for finishing an APM span.
type FinishAPMSpanConfig struct {
	// FinishTime is the finish time for the span.
	FinishTime time.Time
	// Error is an error to set on the span when finishing.
	Error error
}

// APMSpan represents the interface for an APM span.
type APMSpan interface {
	// Finish finishes the span with the given configuration.
	Finish(cfg FinishAPMSpanConfig)
	// AddLink adds a span link to this span.
	AddLink(link SpanLink)
	// SpanID returns the span ID.
	SpanID() string
	// TraceID returns the trace ID.
	TraceID() string
	// SetBaggageItem sets a baggage item on the span.
	SetBaggageItem(key string, value string)
	// BaggageItem returns the baggage item value for the given key.
	BaggageItem(key string) string
}

// SpanLink represents a link between spans. Its IDs are the tracer's native
// numeric IDs; they are formatted as decimal strings on the transport wire (see
// toTransportSpanLinks). The snake-case json tags are part of the public
// llmobs.SpanLink alias's contract — callers persist and replay links via
// encoding/json — so they must not be renamed or dropped.
type SpanLink struct {
	TraceID     uint64            `json:"trace_id"`
	TraceIDHigh uint64            `json:"trace_id_high,omitempty"`
	SpanID      uint64            `json:"span_id"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Tracestate  string            `json:"tracestate,omitempty"`
	Flags       uint32            `json:"flags,omitempty"`
}
