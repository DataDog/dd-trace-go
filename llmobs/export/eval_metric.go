// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// MetricType is the type of an evaluation metric value.
type MetricType string

// Evaluation metric types recognized by LLM Obs.
const (
	MetricTypeCategorical MetricType = "categorical"
	MetricTypeScore       MetricType = "score"
	MetricTypeBoolean     MetricType = "boolean"
	// MetricTypeJSON is a structured metric whose value is a json_value object
	// (e.g. Trajectory's range/segment markers). It must be paired with JSONValue.
	MetricTypeJSON MetricType = "json"
)

// EvaluationMetric is a caller-built LLM Obs evaluation metric to export.
//
// It is joined to a span either by span ID (SpanID + TraceID, both required) or
// by a tag (TagKey + TagValue). Exactly one join family and exactly one value
// (CategoricalValue, ScoreValue, BooleanValue, or JSONValue) must be set.
type EvaluationMetric struct {
	// Span join (both required to use span-based joining).
	SpanID  string
	TraceID string
	// Tag join (both required to use tag-based joining).
	TagKey   string
	TagValue string

	// Label is the metric name (required).
	Label string
	// MetricType is the metric type (MetricTypeCategorical/Score/Boolean/JSON).
	// When empty it is derived from the value kind (MetricTypeJSON for a
	// JSONValue); a MetricType that disagrees with the value kind is rejected.
	MetricType MetricType

	// Exactly one of the following must be set.
	CategoricalValue *string
	ScoreValue       *float64
	BooleanValue     *bool
	JSONValue        map[string]any

	// Timestamp is the evaluation time; a zero Timestamp omits it.
	Timestamp time.Time
	MLApp     string
	Tags      []string

	// Optional narrative/structured fields.
	Assessment string
	Reasoning  string
	Metadata   map[string]any
}

// lower validates the metric and lowers it to the internal transport metric
// (reusing internal/llmobs/transport.LLMObsMetric as the single eval-wire
// source of truth). It returns a non-empty reason string when the metric is
// invalid (and must not be sent).
func (m EvaluationMetric) lower(defaultMLApp string) (*transport.LLMObsMetric, string) {
	if m.Label == "" {
		return nil, "missing label"
	}

	hasSpanJoin := m.SpanID != "" || m.TraceID != ""
	hasTagJoin := m.TagKey != "" || m.TagValue != ""
	switch {
	case hasSpanJoin && hasTagJoin:
		return nil, "both span and tag join provided; set exactly one"
	case hasSpanJoin:
		if m.SpanID == "" || m.TraceID == "" {
			return nil, "span join requires both span_id and trace_id"
		}
	case hasTagJoin:
		if m.TagKey == "" || m.TagValue == "" {
			return nil, "tag join requires both key and value"
		}
	default:
		return nil, "missing join: set span_id+trace_id or tag key+value"
	}

	values := 0
	if m.CategoricalValue != nil {
		values++
	}
	if m.ScoreValue != nil {
		values++
	}
	if m.BooleanValue != nil {
		values++
	}
	if m.JSONValue != nil {
		values++
	}
	if values != 1 {
		return nil, "exactly one of categorical, score, boolean, or json value must be set"
	}
	if m.JSONValue != nil && len(m.JSONValue) == 0 {
		// An empty map counts as "set" above, but json:omitempty drops json_value
		// from the wire, sending a value-less metric that the intake rejects for the
		// whole chunk. Reject it here as a row-level error instead.
		return nil, "json_value must not be empty"
	}
	if m.ScoreValue != nil && (math.IsNaN(*m.ScoreValue) || math.IsInf(*m.ScoreValue, 0)) {
		// encoding/json cannot marshal NaN/Inf; reject this row so it does not fail
		// the whole chunk's marshal.
		return nil, "score value must be a finite number"
	}

	// valueType is the metric type implied by the value kind. Exactly one value is
	// set (checked above), so it is always non-empty; a JSONValue implies
	// MetricTypeJSON (json_value pairs only with metric_type "json", never with a
	// scalar type — that would emit a value-less scalar metric intake rejects).
	var valueType MetricType
	switch {
	case m.CategoricalValue != nil:
		valueType = MetricTypeCategorical
	case m.ScoreValue != nil:
		valueType = MetricTypeScore
	case m.BooleanValue != nil:
		valueType = MetricTypeBoolean
	case m.JSONValue != nil:
		valueType = MetricTypeJSON
	}

	metricType := m.MetricType
	switch {
	case metricType == "":
		metricType = valueType
	case metricType != MetricTypeCategorical && metricType != MetricTypeScore && metricType != MetricTypeBoolean && metricType != MetricTypeJSON:
		return nil, fmt.Sprintf("invalid MetricType %q (want categorical, score, boolean, or json)", metricType)
	case metricType != valueType:
		return nil, fmt.Sprintf("MetricType %q does not match the %s value provided", metricType, valueType)
	}

	mlApp := m.MLApp
	if mlApp == "" {
		mlApp = defaultMLApp
	}

	w := &transport.LLMObsMetric{
		Label:            m.Label,
		MetricType:       string(metricType),
		TimestampMS:      timestampMS(m.Timestamp),
		MLApp:            mlApp,
		Tags:             withTracerVersion(m.Tags),
		Assessment:       m.Assessment,
		Reasoning:        m.Reasoning,
		Metadata:         m.Metadata,
		CategoricalValue: m.CategoricalValue,
		ScoreValue:       m.ScoreValue,
		BooleanValue:     m.BooleanValue,
		JSONValue:        m.JSONValue,
	}
	if hasSpanJoin {
		w.JoinOn.Span = &transport.EvaluationSpanJoin{SpanID: m.SpanID, TraceID: m.TraceID}
	} else {
		w.JoinOn.Tag = &transport.EvaluationTagJoin{Key: m.TagKey, Value: m.TagValue}
	}
	return w, ""
}

// timestampMS returns the Unix-millisecond timestamp, or 0 for a zero time so
// the optional field is omitted.
func timestampMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// withTracerVersion strips any caller-provided ddtrace.version tag and appends
// the SDK's version, matching the live evaluation path so intake can attribute
// the emitting SDK version.
func withTracerVersion(tags []string) []string {
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		if !strings.HasPrefix(t, "ddtrace.version:") {
			out = append(out, t)
		}
	}
	return append(out, "ddtrace.version:"+tracerVersion())
}
