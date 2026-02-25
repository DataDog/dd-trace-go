// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

// ParamType represents the type of a parameter for UI rendering.
type ParamType string

const (
	// ParamTypeString renders as a text input.
	ParamTypeString ParamType = "string"
	// ParamTypeNumber renders as a number input or slider (when Min/Max are set).
	ParamTypeNumber ParamType = "number"
	// ParamTypeBool renders as a toggle switch.
	ParamTypeBool ParamType = "bool"
	// ParamTypePrompt renders as a rich multiline prompt editor.
	ParamTypePrompt ParamType = "prompt"
)

// ParamDef defines a single configurable parameter for an experiment.
// The UI uses these definitions to render appropriate editor widgets.
type ParamDef struct {
	Type        ParamType `json:"type"`
	Default     any       `json:"default"`
	Description string    `json:"description,omitempty"`
	Choices     []any     `json:"choices,omitempty"`
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
}

// defaultsFromParams extracts default values from parameter definitions
// into a flat map suitable for passing to experiment.WithExperimentConfig.
func defaultsFromParams(params map[string]*ParamDef) map[string]any {
	if len(params) == 0 {
		return nil
	}
	defaults := make(map[string]any, len(params))
	for k, p := range params {
		defaults[k] = p.Default
	}
	return defaults
}
