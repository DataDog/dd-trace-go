// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import "github.com/DataDog/dd-trace-go/v2/llmobs"

// MetricType is the type of an evaluation metric value.
type MetricType = llmobs.EvalMetricType

const (
	MetricTypeCategorical MetricType = llmobs.EvalMetricTypeCategorical
	MetricTypeScore       MetricType = llmobs.EvalMetricTypeScore
	MetricTypeBoolean     MetricType = llmobs.EvalMetricTypeBoolean
	MetricTypeJSON        MetricType = llmobs.EvalMetricTypeJSON
)

// EvaluationMetric is a caller-built LLM Obs evaluation metric.
type EvaluationMetric = llmobs.EvaluationMetric
