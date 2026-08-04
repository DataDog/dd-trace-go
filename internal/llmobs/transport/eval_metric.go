// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
)

// EvalMetricType identifies an evaluation metric value type.
type EvalMetricType string

const (
	EvalMetricTypeCategorical EvalMetricType = "categorical"
	EvalMetricTypeScore       EvalMetricType = "score"
	EvalMetricTypeBoolean     EvalMetricType = "boolean"
	EvalMetricTypeJSON        EvalMetricType = "json"
)

// EvaluationJoinOn represents how to join evaluation metrics to spans.
// Exactly one of Span or Tag should be provided.
type EvaluationJoinOn struct {
	// Span contains span and trace IDs for direct span joining.
	Span *EvaluationSpanJoin `json:"span,omitempty"`
	// Tag contains tag key-value for tag-based joining.
	Tag *EvaluationTagJoin `json:"tag,omitempty"`
}

// EvaluationSpanJoin represents joining by span and trace ID.
type EvaluationSpanJoin struct {
	// SpanID is the span ID to join on.
	SpanID string `json:"span_id"`
	// TraceID is the trace ID to join on.
	TraceID string `json:"trace_id"`
}

// EvaluationTagJoin represents joining by tag key-value pairs.
type EvaluationTagJoin struct {
	// Key is the tag key to search for.
	Key string `json:"key"`
	// Value is the tag value to match.
	Value string `json:"value"`
}

// LLMObsMetric represents an evaluation metric for LLMObs spans.
type LLMObsMetric struct {
	JoinOn             EvaluationJoinOn `json:"join_on"`
	MetricType         EvalMetricType   `json:"metric_type,omitempty"`
	Label              string           `json:"label,omitempty"`
	CategoricalValue   *string          `json:"categorical_value,omitempty"`
	ScoreValue         *float64         `json:"score_value,omitempty"`
	BooleanValue       *bool            `json:"boolean_value,omitempty"`
	JSONValue          map[string]any   `json:"json_value,omitempty"`
	MLApp              string           `json:"ml_app,omitempty"`
	TimestampMS        int64            `json:"timestamp_ms,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	Assessment         string           `json:"assessment,omitempty"`
	Reasoning          string           `json:"reasoning,omitempty"`
	EvalMetricMetadata map[string]any   `json:"eval_metric_metadata,omitempty"`
}

type PushMetricsRequest struct {
	Data PushMetricsRequestData `json:"data"`
}

type PushMetricsRequestData struct {
	Type       string                           `json:"type"`
	Attributes PushMetricsRequestDataAttributes `json:"attributes"`
}

type PushMetricsRequestDataAttributes struct {
	Metrics []*LLMObsMetric `json:"metrics"`
}

// NewPushMetricsRequest builds an evaluation metric envelope.
func NewPushMetricsRequest(metrics []*LLMObsMetric) *PushMetricsRequest {
	return &PushMetricsRequest{
		Data: PushMetricsRequestData{
			Type: "evaluation_metric",
			Attributes: PushMetricsRequestDataAttributes{
				Metrics: metrics,
			},
		},
	}
}

func (c *Transport) PushEvalMetrics(
	ctx context.Context,
	metrics []*LLMObsMetric,
) error {
	_, err := c.PushEvalMetricsWithResult(ctx, metrics)
	return err
}

// PushEvalMetricsWithResult sends evaluation metrics and returns request details.
func (c *Transport) PushEvalMetricsWithResult(
	ctx context.Context,
	metrics []*LLMObsMetric,
) (RequestResult, error) {
	if len(metrics) == 0 {
		return RequestResult{}, nil
	}
	body, err := encodeJSON(NewPushMetricsRequest(metrics))
	if err != nil {
		return RequestResult{}, fmt.Errorf("failed to json encode body: %w", err)
	}
	return c.PushEvalMetricsBodyWithResult(ctx, body.Bytes())
}

// PushEvalMetricsBodyWithResult sends an encoded evaluation metric request.
func (c *Transport) PushEvalMetricsBodyWithResult(ctx context.Context, body []byte) (RequestResult, error) {
	if len(body) == 0 {
		return RequestResult{}, nil
	}
	result, err := c.request(
		ctx,
		http.MethodPost,
		endpointEvalMetric,
		subdomainEvalMetric,
		bytes.NewReader(body),
		"application/json",
		defaultLimits,
	)
	if err != nil {
		return summarizeRequest(result), err
	}
	if result.statusCode != http.StatusOK && result.statusCode != http.StatusAccepted {
		return summarizeRequest(result), fmt.Errorf("unexpected status %d: %s", result.statusCode, string(result.body))
	}
	return summarizeRequest(result), nil
}
