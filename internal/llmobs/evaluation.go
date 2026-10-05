// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// EvalMetricType represents an evaluation metric value type.
type EvalMetricType = transport.EvalMetricType

const (
	EvalMetricTypeCategorical EvalMetricType = transport.EvalMetricTypeCategorical
	EvalMetricTypeScore       EvalMetricType = transport.EvalMetricTypeScore
	EvalMetricTypeBoolean     EvalMetricType = transport.EvalMetricTypeBoolean
	EvalMetricTypeJSON        EvalMetricType = transport.EvalMetricTypeJSON
)

// EvaluationConfig contains configuration for submitting evaluation metrics.
type EvaluationConfig struct {
	SpanID   string
	TraceID  string
	TagKey   string
	TagValue string

	Label      string
	MetricType EvalMetricType

	CategoricalValue *string
	ScoreValue       *float64
	BooleanValue     *bool
	JSONValue        map[string]any

	Tags        []string
	MLApp       string
	TimestampMS int64
	Timestamp   time.Time
	Assessment  string
	Reasoning   string
	Metadata    map[string]any
}

var (
	errInvalidMetricLabel  = errors.New("label is required for evaluation metrics")
	errInvalidMetricValue  = errors.New("exactly one metric value (categorical, score, or boolean) must be provided")
	errEvalJoinBothPresent = errors.New("provide either span/trace IDs or tag key/value, not both")
	errEvalJoinNonePresent = errors.New("must provide either span/trace IDs or tag key/value for joining")
	errInvalidSpanJoin     = errors.New("both span and trace IDs are required for span-based joining")
	errInvalidTagJoin      = errors.New("both tag key and value are required for tag-based joining")
)

type evaluationValidationKind uint8

const (
	evaluationMissingLabel evaluationValidationKind = iota
	evaluationInvalidJoin
	evaluationInvalidValue
	evaluationTypeMismatch
)

type evaluationValidationError struct {
	kind   evaluationValidationKind
	reason string
	cause  error
}

func (e *evaluationValidationError) Error() string {
	return e.reason
}

func (e *evaluationValidationError) Unwrap() error {
	return e.cause
}

// SubmitEvaluation submits an evaluation metric for a span.
// The span can be identified either by span/trace IDs or by tag key-value pairs.
func (l *LLMObs) SubmitEvaluation(cfg EvaluationConfig) (err error) {
	var metric *transport.LLMObsMetric
	defer func() {
		trackSubmitEvaluationMetric(metric, err)
	}()

	metric, validation := buildEvaluation(cfg, l.Config.MLApp)
	if validation != nil {
		if validation.cause != nil {
			return validation.cause
		}
		if validation.kind == evaluationInvalidValue {
			return errInvalidMetricValue
		}
		return validation
	}

	l.evalMetricsCh <- metric
	return nil
}

func buildEvaluation(metric EvaluationConfig, defaultMLApp string) (*transport.LLMObsMetric, *evaluationValidationError) {
	joinOn, valueType, validation := validateEvaluation(metric)
	if validation != nil {
		return nil, validation
	}
	metricType, validation := resolveEvaluationMetricType(metric.MetricType, valueType)
	if validation != nil {
		return nil, validation
	}
	return lowerEvaluation(metric, defaultMLApp, joinOn, metricType), nil
}

func validateEvaluation(metric EvaluationConfig) (transport.EvaluationJoinOn, EvalMetricType, *evaluationValidationError) {
	if metric.Label == "" {
		return transport.EvaluationJoinOn{}, "", &evaluationValidationError{
			kind: evaluationMissingLabel, reason: "missing label", cause: errInvalidMetricLabel,
		}
	}

	joinOn, err := buildEvaluationJoin(metric.SpanID, metric.TraceID, metric.TagKey, metric.TagValue)
	if err != nil {
		return transport.EvaluationJoinOn{}, "", &evaluationValidationError{
			kind: evaluationInvalidJoin, reason: err.Error(), cause: err,
		}
	}

	values := 0
	if metric.CategoricalValue != nil {
		values++
	}
	if metric.ScoreValue != nil {
		values++
	}
	if metric.BooleanValue != nil {
		values++
	}
	if metric.JSONValue != nil {
		values++
	}
	if values != 1 {
		return transport.EvaluationJoinOn{}, "", &evaluationValidationError{
			kind: evaluationInvalidValue, reason: "exactly one of categorical, score, boolean, or json value must be set",
		}
	}
	if metric.JSONValue != nil && len(metric.JSONValue) == 0 {
		return transport.EvaluationJoinOn{}, "", &evaluationValidationError{
			kind: evaluationInvalidValue, reason: "json_value must not be empty",
		}
	}

	return joinOn, evaluationValueType(metric), nil
}

func resolveEvaluationMetricType(metricType, valueType EvalMetricType) (EvalMetricType, *evaluationValidationError) {
	switch {
	case metricType == "":
		return valueType, nil
	case metricType != EvalMetricTypeCategorical &&
		metricType != EvalMetricTypeScore &&
		metricType != EvalMetricTypeBoolean &&
		metricType != EvalMetricTypeJSON:
		return "", &evaluationValidationError{
			kind:   evaluationTypeMismatch,
			reason: fmt.Sprintf("invalid metric type %q (want categorical, score, boolean, or json)", metricType),
		}
	case metricType != valueType:
		return "", &evaluationValidationError{
			kind:   evaluationTypeMismatch,
			reason: fmt.Sprintf("metric type %q does not match the %s value provided", metricType, valueType),
		}
	default:
		return metricType, nil
	}
}

func lowerEvaluation(metric EvaluationConfig, defaultMLApp string, joinOn transport.EvaluationJoinOn, metricType EvalMetricType) *transport.LLMObsMetric {
	mlApp := metric.MLApp
	if mlApp == "" {
		mlApp = defaultMLApp
	}
	return &transport.LLMObsMetric{
		JoinOn:             joinOn,
		Label:              metric.Label,
		MetricType:         metricType,
		TimestampMS:        evaluationTimestamp(metric),
		MLApp:              mlApp,
		Tags:               evaluationTags(metric.Tags),
		Assessment:         metric.Assessment,
		Reasoning:          metric.Reasoning,
		EvalMetricMetadata: metric.Metadata,
		CategoricalValue:   metric.CategoricalValue,
		ScoreValue:         metric.ScoreValue,
		BooleanValue:       metric.BooleanValue,
		JSONValue:          metric.JSONValue,
	}
}

func buildEvaluationJoin(spanID, traceID, tagKey, tagValue string) (transport.EvaluationJoinOn, error) {
	hasSpanJoin := false
	if spanID != "" || traceID != "" {
		if spanID == "" || traceID == "" {
			return transport.EvaluationJoinOn{}, errInvalidSpanJoin
		}
		hasSpanJoin = true
	}
	hasTagJoin := false
	if tagKey != "" || tagValue != "" {
		if tagKey == "" || tagValue == "" {
			return transport.EvaluationJoinOn{}, errInvalidTagJoin
		}
		hasTagJoin = true
	}
	switch {
	case hasSpanJoin && hasTagJoin:
		return transport.EvaluationJoinOn{}, errEvalJoinBothPresent
	case hasSpanJoin:
		return transport.EvaluationJoinOn{Span: &transport.EvaluationSpanJoin{SpanID: spanID, TraceID: traceID}}, nil
	case hasTagJoin:
		return transport.EvaluationJoinOn{Tag: &transport.EvaluationTagJoin{Key: tagKey, Value: tagValue}}, nil
	default:
		return transport.EvaluationJoinOn{}, errEvalJoinNonePresent
	}
}

func evaluationValueType(metric EvaluationConfig) EvalMetricType {
	switch {
	case metric.CategoricalValue != nil:
		return EvalMetricTypeCategorical
	case metric.ScoreValue != nil:
		return EvalMetricTypeScore
	case metric.BooleanValue != nil:
		return EvalMetricTypeBoolean
	default:
		return EvalMetricTypeJSON
	}
}

func evaluationTimestamp(metric EvaluationConfig) int64 {
	if metric.TimestampMS != 0 {
		return metric.TimestampMS
	}
	if !metric.Timestamp.IsZero() {
		return metric.Timestamp.UnixMilli()
	}
	return time.Now().UnixMilli()
}

func evaluationTags(tags []string) []string {
	prefix := tagKeyTracerVersion + ":"
	out := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if !strings.HasPrefix(tag, prefix) {
			out = append(out, tag)
		}
	}
	return append(out, prefix+version.Tag)
}
