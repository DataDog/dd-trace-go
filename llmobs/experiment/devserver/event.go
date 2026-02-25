// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

import (
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// StreamEvent is a single event in the newline-delimited JSON stream
// sent by the /eval endpoint.
type StreamEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// StartEventData is the payload for the "start" event.
type StartEventData struct {
	ExperimentName string `json:"experiment_name"`
	ProjectName    string `json:"project_name"`
	ExperimentID   string `json:"experiment_id"`
	DatasetName    string `json:"dataset_name"`
	TotalRows      int    `json:"total_rows"`
}

// ProgressEventData is the payload for "progress" events.
type ProgressEventData struct {
	RowIndex       int                                    `json:"row_index"`
	Status         string                                 `json:"status"`
	Input          any                                    `json:"input,omitempty"`
	ExpectedOutput any                                    `json:"expected_output,omitempty"`
	Output         any                                    `json:"output,omitempty"`
	Error          *ErrorData                             `json:"error,omitempty"`
	Evaluations    map[string]any                         `json:"evaluations,omitempty"`
	Span           *transport.LLMObsSpanEvent             `json:"span,omitempty"`
	EvalMetrics    []transport.ExperimentEvalMetricEvent   `json:"eval_metrics,omitempty"`
}

// SummaryEventData is the payload for the "summary" event.
type SummaryEventData struct {
	Scores  map[string]any `json:"scores"`
	Metrics map[string]any `json:"metrics"`
}

// ErrorData represents an error in a stream event.
type ErrorData struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
}
