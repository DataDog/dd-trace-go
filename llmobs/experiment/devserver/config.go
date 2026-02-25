// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

// ConfigFieldType represents the type of a config field for UI rendering.
type ConfigFieldType string

const (
	// ConfigFieldString renders as a text input.
	ConfigFieldString ConfigFieldType = "string"
	// ConfigFieldNumber renders as a number input or slider (when Min/Max are set).
	ConfigFieldNumber ConfigFieldType = "number"
	// ConfigFieldBool renders as a toggle switch.
	ConfigFieldBool ConfigFieldType = "bool"
	// ConfigFieldPrompt renders as a rich multiline prompt editor.
	ConfigFieldPrompt ConfigFieldType = "prompt"
)

// ConfigField defines a single configurable field for an experiment.
// The UI uses these definitions to render appropriate editor widgets.
type ConfigField struct {
	Type        ConfigFieldType `json:"type"`
	Default     any             `json:"default"`
	Description string          `json:"description,omitempty"`
	Choices     []any           `json:"choices,omitempty"`
	Min         *float64        `json:"min,omitempty"`
	Max         *float64        `json:"max,omitempty"`
}

// defaultsFromConfig extracts default values from config field definitions
// into a flat map suitable for passing to experiment.WithExperimentConfig.
func defaultsFromConfig(params map[string]*ConfigField) map[string]any {
	if len(params) == 0 {
		return nil
	}
	defaults := make(map[string]any, len(params))
	for k, p := range params {
		defaults[k] = p.Default
	}
	return defaults
}
