// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type supportedConfigEntry struct {
	Implementation string   `json:"implementation"`
	Type           string   `json:"type"`
	Default        *string  `json:"default"`
	Aliases        []string `json:"aliases,omitempty"`
}

type supportedConfigsFile struct {
	Version                 string                            `json:"version"`
	SupportedConfigurations map[string][]supportedConfigEntry `json:"supportedConfigurations"`
}

// loadKnown returns the set of every DD_* env var (and its aliases) declared
// in internal/env/supported_configurations.json. The returned map is a set
// (value is struct{}).
func loadKnown(path string) (map[string]struct{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f supportedConfigsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := make(map[string]struct{}, len(f.SupportedConfigurations))
	for key, entries := range f.SupportedConfigurations {
		out[key] = struct{}{}
		for _, e := range entries {
			for _, alias := range e.Aliases {
				out[alias] = struct{}{}
			}
		}
	}
	return out, nil
}
