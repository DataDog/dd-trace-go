// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import "encoding/json"

// defaultParentID mirrors the LLM Obs convention for a span with no parent.
const defaultParentID = "undefined"

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

	// StartNanos is the span start time in Unix nanoseconds.
	StartNanos int64
	// DurationNanos is the span duration in nanoseconds.
	DurationNanos int64

	// Status is "ok" or "error"; empty defaults to "ok".
	Status        string
	StatusMessage string

	// Kind is the LLM Obs span kind (e.g. "llm", "workflow", "agent", "tool").
	Kind          string
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
	InputTokens            *int64 `json:"input_tokens,omitempty"`
	OutputTokens           *int64 `json:"output_tokens,omitempty"`
	TotalTokens            *int64 `json:"total_tokens,omitempty"`
	CacheWriteInputTokens  *int64 `json:"cache_write_input_tokens,omitempty"`
	CacheReadInputTokens   *int64 `json:"cache_read_input_tokens,omitempty"`
	NonCachedInputTokens   *int64 `json:"non_cached_input_tokens,omitempty"`
	ReasoningOutputTokens  *int64 `json:"reasoning_output_tokens,omitempty"`
	Ephemeral1HInputTokens *int64 `json:"ephemeral_1h_input_tokens,omitempty"`
	Ephemeral5MInputTokens *int64 `json:"ephemeral_5m_input_tokens,omitempty"`
	BillableCharacterCount *int64 `json:"billable_character_count,omitempty"`

	TimeToFirstToken *float64 `json:"time_to_first_token,omitempty"`

	EstimatedTotalCost           *float64 `json:"estimated_total_cost,omitempty"`
	EstimatedInputCost           *float64 `json:"estimated_input_cost,omitempty"`
	EstimatedOutputCost          *float64 `json:"estimated_output_cost,omitempty"`
	EstimatedCacheReadInputCost  *float64 `json:"estimated_cache_read_input_cost,omitempty"`
	EstimatedCacheWriteInputCost *float64 `json:"estimated_cache_write_input_cost,omitempty"`

	// Extra carries any metric keys not covered by the named fields (custom or
	// newly-standardized keys). Keys are emitted verbatim alongside the named
	// fields; a named field wins on key collision.
	Extra map[string]float64 `json:"-"`
}

// MarshalJSON emits the named metric fields and merges in Extra so arbitrary
// metric keys survive, matching the flat key -> number wire shape. A named field
// takes precedence over an Extra entry with the same key.
func (m SpanMetrics) MarshalJSON() ([]byte, error) {
	type alias SpanMetrics // no MarshalJSON -> default struct encoding of named fields
	if len(m.Extra) == 0 {
		return json.Marshal(alias(m))
	}
	// Merge Extra with the named fields in a single marshal (named wins on key
	// collision), avoiding a marshal/unmarshal/marshal round-trip. The keys mirror
	// the struct tags above; the wire-shape contract test guards against drift.
	out := make(map[string]any, len(m.Extra)+16)
	for k, v := range m.Extra {
		out[k] = v
	}
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
	return json.Marshal(out)
}

// putInt/putFloat set a metric key only when the pointer is non-nil, mirroring
// the named fields' omitempty behavior.
func putInt(m map[string]any, key string, v *int64) {
	if v != nil {
		m[key] = *v
	}
}

func putFloat(m map[string]any, key string, v *float64) {
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

// ---- wire types (Trajectory's production-proven /api/v2/llmobs shape) ----

type wireSpanPayload struct {
	DDStage         string     `json:"_dd.stage"`
	DDTracerVersion string     `json:"_dd.tracer_version"`
	EventType       string     `json:"event_type"`
	Spans           []wireSpan `json:"spans"`
}

type wireSpan struct {
	TraceID          string         `json:"trace_id"`
	SpanID           string         `json:"span_id"`
	ParentID         string         `json:"parent_id"`
	SessionID        string         `json:"session_id,omitempty"`
	Name             string         `json:"name"`
	Service          string         `json:"service,omitempty"`
	StartNs          int64          `json:"start_ns"`
	Duration         int64          `json:"duration"`
	Status           string         `json:"status"`
	StatusMessage    string         `json:"status_message,omitempty"`
	Meta             wireMeta       `json:"meta"`
	Metrics          *SpanMetrics   `json:"metrics,omitempty"`
	Tags             []string       `json:"tags"`
	SpanLinks        []wireSpanLink `json:"span_links,omitempty"`
	CollectionErrors []string       `json:"collection_errors,omitempty"`
	DD               wireSpanDD     `json:"_dd"`
}

type wireMeta struct {
	// Span kind is emitted in BOTH forms for maximum intake compatibility:
	//   - nested meta.span.kind: the storage schema (llmobs-internal Meta.Span) and
	//     Trajectory's production payload (ddllmobs LLMObsMeta.Span).
	//   - flat meta."span.kind": what the live tracer writes
	//     (internal/llmobs/llmobs.go), i.e. what the current SDK/intake path reads.
	// Both carry the same value; a reader that only knows one form still gets the kind.
	Span          wireMetaSpan   `json:"span"`
	SpanKindFlat  string         `json:"span.kind,omitempty"`
	ModelName     string         `json:"model_name,omitempty"`
	ModelProvider string         `json:"model_provider,omitempty"`
	Input         *wireIO        `json:"input,omitempty"`
	Output        *wireIO        `json:"output,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type wireMetaSpan struct {
	Kind string `json:"kind"`
}

type wireIO struct {
	Value string `json:"value"`
}

type wireSpanLink struct {
	SpanID     string            `json:"span_id"`
	TraceID    string            `json:"trace_id"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// wireSpanDD is the span's _dd block. Unlike the shared transport.DDAttributes,
// apm_trace_id is omitempty so an offline span that is not correlated to APM
// omits the optional field rather than sending apm_trace_id:"".
type wireSpanDD struct {
	SpanID     string `json:"span_id"`
	TraceID    string `json:"trace_id"`
	APMTraceID string `json:"apm_trace_id,omitempty"`
}

// toWire lowers a public SpanEvent to the wire shape, applying defaults.
func (e SpanEvent) toWire(defaultService string) wireSpan {
	parentID := e.ParentID
	if parentID == "" {
		parentID = defaultParentID
	}
	name := e.Name
	if name == "" {
		name = e.Kind
	}
	status := e.Status
	if status == "" {
		status = "ok"
	}
	service := e.Service
	if service == "" {
		service = defaultService
	}

	ws := wireSpan{
		TraceID:       e.TraceID,
		SpanID:        e.SpanID,
		ParentID:      parentID,
		SessionID:     e.SessionID,
		Name:          name,
		Service:       service,
		StartNs:       e.StartNanos,
		Duration:      e.DurationNanos,
		Status:        status,
		StatusMessage: e.StatusMessage,
		Meta: wireMeta{
			Span:          wireMetaSpan{Kind: e.Kind},
			SpanKindFlat:  e.Kind,
			ModelName:     e.ModelName,
			ModelProvider: e.ModelProvider,
			Metadata:      e.Metadata,
		},
		Metrics: e.Metrics,
		DD: wireSpanDD{
			SpanID:     e.SpanID,
			TraceID:    e.TraceID,
			APMTraceID: e.APMTraceID,
		},
	}
	if e.Input != "" {
		ws.Meta.Input = &wireIO{Value: e.Input}
	}
	if e.Output != "" {
		ws.Meta.Output = &wireIO{Value: e.Output}
	}
	// Clone the caller's tags so later stamping (env/version) never mutates the
	// caller's backing array, and so tags is always non-nil in the payload.
	ws.Tags = append([]string{}, e.Tags...)
	for _, l := range e.SpanLinks {
		ws.SpanLinks = append(ws.SpanLinks, wireSpanLink{
			SpanID:     l.SpanID,
			TraceID:    l.TraceID,
			Attributes: l.Attributes,
		})
	}
	return ws
}
