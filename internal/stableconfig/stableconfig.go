// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

// stableConfig represents a configuration loaded from a YAML source file.
type stableConfig struct {
	Config map[string]string `yaml:"apm_configuration_default,omitempty"` // Configuration key-value pairs.
	ID     int               `yaml:"config_id,omitempty"`                 // Identifier for the config set.
}

func (s *stableConfig) get(key string) string {
	return s.Config[key]
}

// isEmpty checks if the config is considered empty (no ID and no config entries).
func (s *stableConfig) isEmpty() bool {
	return s.ID == -1 && len(s.Config) == 0
}

// emptyStableConfig creates and returns a new, empty stableConfig instance.
func emptyStableConfig() *stableConfig {
	return &stableConfig{
		Config: make(map[string]string, 0),
		ID:     -1,
	}
}
