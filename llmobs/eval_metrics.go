// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"time"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// EvaluationValue represents the allowed types for evaluation metric values.
type EvaluationValue interface {
	~bool | ~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// SubmitEvaluationFromSpan submits an evaluation metric for the given span.
func SubmitEvaluationFromSpan[T EvaluationValue](label string, value T, span BaseSpan, opts ...EvaluationOption) {
	cfg := illmobs.EvaluationConfig{
		Label:   label,
		SpanID:  span.SpanID(),
		TraceID: span.TraceID(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	setValueFromGeneric(&cfg, value)

	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to submit evaluation metric: %v", err)
		return
	}
	if err := ll.SubmitEvaluation(cfg); err != nil {
		log.Warn("llmobs: failed to submit evaluation metric: %v", err)
	}
}

type JoinTag struct {
	Key   string
	Value string
}

// SubmitEvaluationFromTag submits an evaluation metric for spans identified by a tag key-value pair.
func SubmitEvaluationFromTag[T EvaluationValue](label string, value T, tag JoinTag, opts ...EvaluationOption) {
	cfg := illmobs.EvaluationConfig{
		Label:    label,
		TagKey:   tag.Key,
		TagValue: tag.Value,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	setValueFromGeneric(&cfg, value)

	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to submit evaluation metric: %v", err)
		return
	}
	if err := ll.SubmitEvaluation(cfg); err != nil {
		log.Warn("llmobs: failed to submit evaluation metric: %v", err)
	}
}

// ------------- Evaluation options -------------

// EvaluationOption configures evaluation metric submission.
type EvaluationOption func(cfg *illmobs.EvaluationConfig)

// WithEvaluationTags sets tags for the evaluation metric.
func WithEvaluationTags(tags []string) EvaluationOption {
	return func(cfg *illmobs.EvaluationConfig) {
		cfg.Tags = tags
	}
}

// WithEvaluationMLApp sets the ML application name for the evaluation metric.
// If not set, uses the global ML app configuration.
func WithEvaluationMLApp(mlApp string) EvaluationOption {
	return func(cfg *illmobs.EvaluationConfig) {
		cfg.MLApp = mlApp
	}
}

// WithEvaluationTimestamp sets a custom timestamp for the evaluation metric.
// If not set, uses the current time.
func WithEvaluationTimestamp(t time.Time) EvaluationOption {
	return func(cfg *illmobs.EvaluationConfig) {
		cfg.TimestampMS = t.UnixMilli()
	}
}

// setValueFromGeneric automatically determines the metric type and sets the appropriate value field using generics.
func setValueFromGeneric[T EvaluationValue](cfg *illmobs.EvaluationConfig, value T) {
	switch v := any(value).(type) {
	case bool:
		cfg.BooleanValue = &v
	case string:
		cfg.CategoricalValue = &v
	case int:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case int8:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case int16:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case int32:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case int64:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case uint:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case uint8:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case uint16:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case uint32:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case uint64:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case float32:
		f64 := float64(v)
		cfg.ScoreValue = &f64
	case float64:
		cfg.ScoreValue = &v
	}
}
