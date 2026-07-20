// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"maps"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// defaultParentID mirrors the LLM Obs convention for a span with no parent.
const defaultParentID = "undefined"

// Kind is the LLM Obs span kind.
type Kind string

// Span kinds recognized by LLM Obs.
const (
	KindLLM       Kind = "llm"
	KindAgent     Kind = "agent"
	KindWorkflow  Kind = "workflow"
	KindTask      Kind = "task"
	KindTool      Kind = "tool"
	KindEmbedding Kind = "embedding"
	KindRetrieval Kind = "retrieval"
)

// Status is the terminal status of a span.
type Status string

// Span statuses recognized by LLM Obs.
const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// SpanEvent is a caller-built LLM Obs span to export. IDs are opaque strings and
// are preserved verbatim; empty ParentID is normalized to "undefined".
type SpanEvent struct {
	// TraceID and SpanID are required, opaque, caller-owned IDs (decimal uint64
	// strings are valid; hex is not required).
	TraceID string
	SpanID  string
	// ParentID is the parent span ID; empty is normalized to "undefined".
	ParentID string
	// APMTraceID optionally sets _dd.apm_trace_id for APM correlation. It is not
	// derived from TraceID.
	APMTraceID string

	SessionID string
	// Name is the span name; when empty, Kind is used.
	Name string
	// Service overrides the client's default service for this span.
	Service string

	// Start is the span start time. A zero Start emits start_ns=0.
	Start time.Time
	// Duration is the span duration.
	Duration time.Duration

	// Status is the span status; empty defaults to StatusOK.
	Status        Status
	StatusMessage string

	// Kind is the LLM Obs span kind (e.g. KindLLM, KindWorkflow).
	Kind          Kind
	ModelName     string
	ModelProvider string
	// Input and Output are the raw string input/output values.
	Input  string
	Output string
	// Metadata is free-form span metadata; values may be strings, numbers,
	// bools, or nested structures (matching the live annotation path).
	Metadata map[string]any

	// Metrics holds optional token/cost metrics.
	Metrics *SpanMetrics
	// Tags are free-form "key:value" tags.
	Tags []string
	// SpanLinks are links to other spans, by opaque string IDs.
	SpanLinks []SpanLink
}

// SpanMetrics holds optional token and cost metrics for a span. Cost fields are
// in the units the caller reports (Trajectory uses nanodollars). All fields are
// optional; nil fields are omitted from the payload.
//
// The named token/count fields correspond to the standard MetricKey* constants
// in llmobs/option.go; the estimated_* fields carry per-span cost. Any
// additional metric key a reconstructed span carries can be passed via Extra so
// nothing is silently dropped; on the wire the internal transport models span
// metrics as a flat map of key -> number
// (internal/llmobs/transport.LLMObsSpanEvent.Metrics), which Extra mirrors.
type SpanMetrics struct {
	InputTokens            *int64
	OutputTokens           *int64
	TotalTokens            *int64
	CacheWriteInputTokens  *int64
	CacheReadInputTokens   *int64
	NonCachedInputTokens   *int64
	ReasoningOutputTokens  *int64
	Ephemeral1HInputTokens *int64
	Ephemeral5MInputTokens *int64
	BillableCharacterCount *int64

	TimeToFirstToken *float64

	EstimatedTotalCost           *float64
	EstimatedInputCost           *float64
	EstimatedOutputCost          *float64
	EstimatedCacheReadInputCost  *float64
	EstimatedCacheWriteInputCost *float64

	// Extra carries any metric keys not covered by the named fields (custom or
	// newly-standardized keys). Keys are emitted verbatim alongside the named
	// fields; a named field wins on key collision.
	Extra map[string]float64
}

// toMetrics flattens the named metric fields (merging in Extra, named wins on
// key collision) into the transport's flat key -> number wire shape. It returns
// nil when nothing is set so metrics is omitted from the payload.
func (m *SpanMetrics) toMetrics() map[string]float64 {
	if m == nil {
		return nil
	}
	out := make(map[string]float64, len(m.Extra)+16)
	maps.Copy(out, m.Extra)
	putInt(out, "input_tokens", m.InputTokens)
	putInt(out, "output_tokens", m.OutputTokens)
	putInt(out, "total_tokens", m.TotalTokens)
	putInt(out, "cache_write_input_tokens", m.CacheWriteInputTokens)
	putInt(out, "cache_read_input_tokens", m.CacheReadInputTokens)
	putInt(out, "non_cached_input_tokens", m.NonCachedInputTokens)
	putInt(out, "reasoning_output_tokens", m.ReasoningOutputTokens)
	putInt(out, "ephemeral_1h_input_tokens", m.Ephemeral1HInputTokens)
	putInt(out, "ephemeral_5m_input_tokens", m.Ephemeral5MInputTokens)
	putInt(out, "billable_character_count", m.BillableCharacterCount)
	putFloat(out, "time_to_first_token", m.TimeToFirstToken)
	putFloat(out, "estimated_total_cost", m.EstimatedTotalCost)
	putFloat(out, "estimated_input_cost", m.EstimatedInputCost)
	putFloat(out, "estimated_output_cost", m.EstimatedOutputCost)
	putFloat(out, "estimated_cache_read_input_cost", m.EstimatedCacheReadInputCost)
	putFloat(out, "estimated_cache_write_input_cost", m.EstimatedCacheWriteInputCost)
	if len(out) == 0 {
		return nil
	}
	return out
}

// putInt/putFloat set a metric key only when the pointer is non-nil, mirroring
// the named fields' omitempty behavior. A named field overwrites any Extra entry
// sharing its key (named wins).
func putInt(m map[string]float64, key string, v *int64) {
	if v != nil {
		m[key] = float64(*v)
	}
}

func putFloat(m map[string]float64, key string, v *float64) {
	if v != nil {
		m[key] = *v
	}
}

// SpanLink links a span to another span by opaque string IDs.
type SpanLink struct {
	SpanID     string
	TraceID    string
	Attributes map[string]string
}

// toWire lowers a public SpanEvent to the shared transport wire shape, applying
// defaults. The structured meta block is a map[string]any so it reproduces the
// exact intake JSON (nested meta.span.kind plus the flat meta."span.kind", model
// fields, input/output, metadata) with the same omitempty behavior as before.
func (e SpanEvent) toWire(defaultService string) *transport.LLMObsSpanEvent {
	parentID := e.ParentID
	if parentID == "" {
		parentID = defaultParentID
	}
	name := e.Name
	if name == "" {
		name = string(e.Kind)
	}
	status := e.Status
	if status == "" {
		status = StatusOK
	}
	service := e.Service
	if service == "" {
		service = defaultService
	}

	// meta.span is always present (nested meta.span.kind: storage schema +
	// Trajectory's production payload); the flat meta."span.kind" mirrors what the
	// live tracer writes. model/input/output/metadata keep omitempty semantics.
	meta := map[string]any{
		"span": map[string]any{"kind": string(e.Kind)},
	}
	if e.Kind != "" {
		meta["span.kind"] = string(e.Kind)
	}
	if e.ModelName != "" {
		meta["model_name"] = e.ModelName
	}
	if e.ModelProvider != "" {
		meta["model_provider"] = e.ModelProvider
	}
	if e.Input != "" {
		meta["input"] = map[string]any{"value": e.Input}
	}
	if e.Output != "" {
		meta["output"] = map[string]any{"value": e.Output}
	}
	if len(e.Metadata) > 0 {
		meta["metadata"] = e.Metadata
	}

	ev := &transport.LLMObsSpanEvent{
		TraceID:       e.TraceID,
		SpanID:        e.SpanID,
		ParentID:      parentID,
		SessionID:     e.SessionID,
		Name:          name,
		Service:       service,
		StartNS:       startNanos(e.Start),
		Duration:      int64(e.Duration),
		Status:        string(status),
		StatusMessage: e.StatusMessage,
		Meta:          meta,
		Metrics:       e.Metrics.toMetrics(),
		DDAttributes: transport.DDAttributes{
			SpanID:     e.SpanID,
			TraceID:    e.TraceID,
			APMTraceID: e.APMTraceID,
		},
	}
	// Clone the caller's tags so later stamping (env/version) never mutates the
	// caller's backing array, and so tags is always non-nil in the payload.
	ev.Tags = append([]string{}, e.Tags...)
	for _, l := range e.SpanLinks {
		ev.SpanLinks = append(ev.SpanLinks, transport.SpanLink{
			SpanID:     transport.StringSpanLinkID(l.SpanID),
			TraceID:    transport.StringSpanLinkID(l.TraceID),
			Attributes: l.Attributes,
		})
	}
	return ev
}

// startNanos returns the Unix-nanosecond start, or 0 for a zero time (UnixNano
// on a zero Time is a large negative sentinel, not a meaningful start).
func startNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}
