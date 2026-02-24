// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

// StreamEvent is a single event in the newline-delimited JSON stream
// sent by the /eval endpoint.
type StreamEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// StartEventData is the payload for the "start" event.
type StartEventData struct {
	ExperimentName string `json:"experimentName"`
	ProjectName    string `json:"projectName"`
	ExperimentID   string `json:"experimentId"`
	DatasetName    string `json:"datasetName"`
	TotalRows      int    `json:"totalRows"`
}

// ProgressEventData is the payload for "progress" events.
type ProgressEventData struct {
	RowIndex    int            `json:"rowIndex"`
	Status      string         `json:"status"`
	Input       any            `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
	Error       *ErrorData     `json:"error,omitempty"`
	Evaluations map[string]any `json:"evaluations,omitempty"`
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
