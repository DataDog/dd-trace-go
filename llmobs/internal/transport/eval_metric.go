// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"fmt"
	"net/http"
)

type LLMObsEvaluationMetricEvent struct {
	JoinOn           map[string]map[string]string `json:"join_on,omitempty"`
	MetricType       string                       `json:"metric_type,omitempty"`
	Label            string                       `json:"label,omitempty"`
	CategoricalValue *string                      `json:"categorical_value,omitempty"`
	ScoreValue       *float64                     `json:"score_value,omitempty"`
	BooleanValue     *bool                        `json:"boolean_value,omitempty"`
	MLApp            string                       `json:"ml_app,omitempty"`
	TimestampMS      int64                        `json:"timestamp_ms,omitempty"`
	Tags             []string                     `json:"tags,omitempty"`
}

func (c *Client) LLMObsEvalMetricsSend(
	ctx context.Context,
	metrics []*LLMObsEvaluationMetricEvent,
) error {
	if len(metrics) == 0 {
		return nil
	}
	path := endpointEvalMetric
	method := http.MethodPost

	status, b, err := c.request(ctx, method, path, subdomainEvalMetric, metrics)
	if err != nil {
		return fmt.Errorf("post llmobs eval metrics failed: %v (status=%d, body=%s)", err, status, string(b))
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", status, string(b))
	}
	return nil
}
