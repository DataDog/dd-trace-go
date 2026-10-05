// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// declarativeConfig represents a configuration loaded from a YAML source file.
type declarativeConfig struct {
	Config map[string]string `yaml:"apm_configuration_default,omitempty"`
	ID     string            `yaml:"config_id,omitempty"`
}

func (d *declarativeConfig) get(key string) string {
	return d.Config[key]
}

func (d *declarativeConfig) getID() string {
	return d.ID
}

func emptyDeclarativeConfig() *declarativeConfig {
	return &declarativeConfig{
		Config: make(map[string]string),
		ID:     telemetry.EmptyID,
	}
}
