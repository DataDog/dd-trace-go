// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func validExportSpan(kind transport.SpanKind) transport.LLMObsSpanEvent {
	return transport.LLMObsSpanEvent{
		TraceID: "trace",
		SpanID:  "span",
		Status:  transport.SpanStatusOK,
		Meta:    llmobs.NewSpanEventMeta(kind),
		SpanLinks: []transport.SpanLink{{
			TraceID:     "18446744073709551615",
			TraceIDHigh: "0",
			SpanID:      "42",
		}},
	}
}

func TestValidateExportSpan(t *testing.T) {
	for _, kind := range []transport.SpanKind{
		transport.SpanKindLLM,
		transport.SpanKindAgent,
		transport.SpanKindWorkflow,
		transport.SpanKindTask,
		transport.SpanKindStep,
		transport.SpanKindTool,
		transport.SpanKindEmbedding,
		transport.SpanKindRetrieval,
	} {
		event := validExportSpan(kind)
		require.Nil(t, llmobs.ValidateExportSpan(event), kind)
	}

	for _, status := range []transport.SpanStatus{"", transport.SpanStatusOK, transport.SpanStatusError} {
		event := validExportSpan(transport.SpanKindLLM)
		event.Status = status
		require.Nil(t, llmobs.ValidateExportSpan(event), status)
	}

	tests := []struct {
		name   string
		mutate func(*transport.LLMObsSpanEvent)
		code   llmobs.ExportValidationCode
		reason string
	}{
		{
			name: "missing ID",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.TraceID = ""
			},
			code: llmobs.ExportCodeMissingID,
		},
		{
			name: "missing kind",
			mutate: func(event *transport.LLMObsSpanEvent) {
				delete(event.Meta, "span.kind")
			},
			code: llmobs.ExportCodeMissingKind,
		},
		{
			name: "invalid kind",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.Meta["span.kind"] = string(transport.SpanKindExperiment)
			},
			code: llmobs.ExportCodeInvalidKind,
		},
		{
			name: "invalid status",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.Status = "pending"
			},
			code: llmobs.ExportCodeInvalidStatus,
		},
		{
			name: "invalid link trace ID",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.SpanLinks[0].TraceID = "01"
			},
			code:   llmobs.ExportCodeInvalidLink,
			reason: "trace_id",
		},
		{
			name: "invalid link span ID",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.SpanLinks[0].SpanID = "+42"
			},
			code:   llmobs.ExportCodeInvalidLink,
			reason: "span_id",
		},
		{
			name: "invalid link high trace ID",
			mutate: func(event *transport.LLMObsSpanEvent) {
				event.SpanLinks[0].TraceIDHigh = "18446744073709551616"
			},
			code:   llmobs.ExportCodeInvalidLink,
			reason: "trace_id_high",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validExportSpan(transport.SpanKindLLM)
			test.mutate(&event)

			validation := llmobs.ValidateExportSpan(event)
			require.NotNil(t, validation)
			assert.Equal(t, test.code, validation.Code)
			if test.reason != "" {
				assert.Contains(t, validation.Reason, test.reason)
			}
		})
	}
}

func TestBuildExportSpan(t *testing.T) {
	event := validExportSpan(transport.SpanKindLLM)
	event.SessionID = "session-new"
	event.Status = transport.SpanStatusError
	event.Tags = []string{
		"service:stale",
		"session_id:stale",
		"env:event",
		"version:",
		"ml_app:event-app",
		"team:ml",
	}
	event.Meta["custom"] = "original"
	event.Meta["error.type"] = "provider.Error"
	event.Metrics = map[string]float64{"tokens": 1}
	event.CollectionErrors = []string{"existing"}

	cfg := &config.Config{
		MLApp: "client-app",
		TracerConfig: config.TracerConfig{
			Env:     "client-env",
			Version: "client-version",
		},
	}
	span := llmobs.BuildExportSpan(event, cfg, "client-service")

	assert.Equal(t, llmobs.DefaultParentID, span.ParentID)
	assert.Equal(t, "llm", span.Name)
	assert.Equal(t, "client-service", span.Service)
	assert.Equal(t, event.SpanID, span.DDAttributes.SpanID)
	assert.Equal(t, event.TraceID, span.DDAttributes.TraceID)
	assert.Contains(t, span.Tags, "service:client-service")
	assert.NotContains(t, span.Tags, "service:stale")
	assert.Contains(t, span.Tags, "session_id:session-new")
	assert.NotContains(t, span.Tags, "session_id:stale")
	assert.Contains(t, span.Tags, "env:event")
	assert.NotContains(t, span.Tags, "env:client-env")
	assert.Contains(t, span.Tags, "version:client-version")
	assert.NotContains(t, span.Tags, "version:")
	assert.Contains(t, span.Tags, "ml_app:event-app")
	assert.Contains(t, span.Tags, "source:integration")
	assert.Contains(t, span.Tags, "language:go")
	assert.Contains(t, span.Tags, "ddtrace.version:"+version.Tag)
	assert.Contains(t, span.Tags, "error:1")
	assert.Contains(t, span.Tags, "error_type:provider.Error")

	span.Tags[0] = "changed"
	span.Meta["custom"] = "changed"
	span.Metrics["tokens"] = 2
	span.CollectionErrors[0] = "changed"
	span.SpanLinks[0].SpanID = "99"
	assert.Equal(t, "service:stale", event.Tags[0])
	assert.Equal(t, "original", event.Meta["custom"])
	assert.Equal(t, float64(1), event.Metrics["tokens"])
	assert.Equal(t, "existing", event.CollectionErrors[0])
	assert.Equal(t, "42", event.SpanLinks[0].SpanID)

	override := validExportSpan(transport.SpanKindStep)
	override.Service = "event-service"
	override.Tags = []string{"service:stale"}
	builtOverride := llmobs.BuildExportSpan(override, cfg, "client-service")
	assert.Equal(t, "event-service", builtOverride.Service)
	assert.Contains(t, builtOverride.Tags, "service:event-service")
	assert.NotContains(t, builtOverride.Tags, "service:stale")
}

func TestBuildExportEvaluation(t *testing.T) {
	categorical := "correct"
	metric, validation := llmobs.BuildExportEvaluation(llmobs.EvaluationConfig{
		SpanID:           "span",
		TraceID:          "trace",
		Label:            "quality",
		CategoricalValue: &categorical,
		TimestampMS:      123,
		Tags:             []string{"team:ml", "ddtrace.version:stale"},
		Assessment:       "pass",
		Reasoning:        "matched",
		Metadata:         map[string]any{"judge": "model"},
	}, "default-app")
	require.Nil(t, validation)
	require.NotNil(t, metric)
	require.NotNil(t, metric.JoinOn.Span)
	assert.Equal(t, "span", metric.JoinOn.Span.SpanID)
	assert.Equal(t, "trace", metric.JoinOn.Span.TraceID)
	assert.Equal(t, transport.EvalMetricTypeCategorical, metric.MetricType)
	assert.Equal(t, "default-app", metric.MLApp)
	assert.Equal(t, int64(123), metric.TimestampMS)
	assert.Equal(t, "pass", metric.Assessment)
	assert.Equal(t, "matched", metric.Reasoning)
	assert.Equal(t, map[string]any{"judge": "model"}, metric.EvalMetricMetadata)
	assert.Equal(t, []string{"team:ml", "ddtrace.version:" + version.Tag}, metric.Tags)

	score := 0.8
	boolean := true
	valid := []struct {
		name       string
		metric     llmobs.EvaluationConfig
		metricType transport.EvalMetricType
	}{
		{
			name: "score",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "score", ScoreValue: &score,
				MetricType: transport.EvalMetricTypeScore, MLApp: "event-app",
				Timestamp: time.UnixMilli(456),
			},
			metricType: transport.EvalMetricTypeScore,
		},
		{
			name: "boolean",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "boolean", BooleanValue: &boolean,
			},
			metricType: transport.EvalMetricTypeBoolean,
		},
		{
			name: "json tag join",
			metric: llmobs.EvaluationConfig{
				TagKey: "session_id", TagValue: "session", Label: "json",
				JSONValue: map[string]any{"answer": 42},
			},
			metricType: transport.EvalMetricTypeJSON,
		},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, validation := llmobs.BuildExportEvaluation(test.metric, "default-app")
			require.Nil(t, validation)
			require.NotNil(t, got)
			assert.Equal(t, test.metricType, got.MetricType)
		})
	}
}

func TestBuildExportEvaluationValidation(t *testing.T) {
	score := 1.0
	categorical := "good"
	tests := []struct {
		name     string
		metric   llmobs.EvaluationConfig
		code     llmobs.ExportValidationCode
		hasCause bool
	}{
		{
			name:     "missing label",
			metric:   llmobs.EvaluationConfig{SpanID: "span", TraceID: "trace", ScoreValue: &score},
			code:     llmobs.ExportCodeMissingLabel,
			hasCause: true,
		},
		{
			name:     "invalid join",
			metric:   llmobs.EvaluationConfig{Label: "quality", ScoreValue: &score},
			code:     llmobs.ExportCodeInvalidJoin,
			hasCause: true,
		},
		{
			name:   "missing value",
			metric: llmobs.EvaluationConfig{SpanID: "span", TraceID: "trace", Label: "quality"},
			code:   llmobs.ExportCodeInvalidValue,
		},
		{
			name: "multiple values",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality",
				ScoreValue: &score, CategoricalValue: &categorical,
			},
			code: llmobs.ExportCodeInvalidValue,
		},
		{
			name: "empty JSON value",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality", JSONValue: map[string]any{},
			},
			code: llmobs.ExportCodeInvalidValue,
		},
		{
			name: "unknown metric type",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality", ScoreValue: &score, MetricType: "scores",
			},
			code: llmobs.ExportCodeTypeMismatch,
		},
		{
			name: "metric type mismatch",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality", ScoreValue: &score,
				MetricType: transport.EvalMetricTypeCategorical,
			},
			code: llmobs.ExportCodeTypeMismatch,
		},
		{
			name: "NaN score",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality", ScoreValue: exportFloatPtr(math.NaN()),
			},
			code: llmobs.ExportCodeInvalidValue,
		},
		{
			name: "infinite score",
			metric: llmobs.EvaluationConfig{
				SpanID: "span", TraceID: "trace", Label: "quality", ScoreValue: exportFloatPtr(math.Inf(1)),
			},
			code: llmobs.ExportCodeInvalidValue,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metric, validation := llmobs.BuildExportEvaluation(test.metric, "default-app")
			assert.Nil(t, metric)
			require.NotNil(t, validation)
			assert.Equal(t, test.code, validation.Code)
			if test.hasCause {
				cause := errors.Unwrap(validation)
				require.Error(t, cause)
				assert.ErrorIs(t, validation, cause)
			} else {
				assert.NoError(t, errors.Unwrap(validation))
			}
		})
	}

	_, validation := llmobs.BuildExportEvaluation(tests[0].metric, "default-app")
	require.NotNil(t, validation)
	validation.Index = 7
	assert.Equal(t, "llmobs/export: row 7 rejected (missing_label): missing label", validation.Error())
	var target *llmobs.ExportValidationError
	require.ErrorAs(t, validation, &target)
	assert.Same(t, validation, target)
}

func TestSubmitEvaluationTypeValidation(t *testing.T) {
	score := 1.0
	observer := &llmobs.LLMObs{Config: &config.Config{}}
	err := observer.SubmitEvaluation(llmobs.EvaluationConfig{
		SpanID:     "span",
		TraceID:    "trace",
		Label:      "quality",
		ScoreValue: &score,
		MetricType: "scores",
	})
	require.ErrorContains(t, err, `invalid metric type "scores"`)
	assert.NoError(t, errors.Unwrap(err))
}

func exportFloatPtr(value float64) *float64 {
	return &value
}
