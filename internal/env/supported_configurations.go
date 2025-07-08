// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"encoding/json"
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// SupportedConfiguration represents the content of the supported-configurations.json file.
type SupportedConfiguration struct {
	SupportedConfigurations map[string][]string `json:"supportedConfigurations"`
	Aliases                 map[string][]string `json:"aliases"`
}

// addSupportedConfigurationToFile adds a supported configuration to the json file.
// it is used only in testing mode.
//
// It reads the json file, adds the new configuration, and writes it back to the file.
// The JSON output will have sorted keys since Go's json.Marshal sorts map keys automatically.
//
// When called with DD_CONFIG_INVERSION_UNKNOWN nothing is done as it is a special value
// used in a unit test to verify the behavior of unknown env var.
func addSupportedConfigurationToFile(name string) {
	if name == "DD_CONFIG_INVERSION_UNKNOWN" {
		// noop for unit test scenario
		return
	}

	// read the json file
	jsonFile, err := os.ReadFile("supported-configurations.json")
	if err != nil {
		log.Error("config: failed to open supported-configurations.json: %v", err)
		return
	}

	var cfg SupportedConfiguration
	if err := json.Unmarshal(jsonFile, &cfg); err != nil {
		log.Error("config: failed to unmarshal supported configuration: %v", err)
		return
	}

	if _, ok := cfg.SupportedConfigurations[name]; !ok {
		cfg.SupportedConfigurations[name] = []string{"A"}
	}

	// write the json file - Go's json.MarshalIndent automatically sorts map keys
	jsonFile, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Error("config: failed to marshal supported configuration: %v", err)
		return
	}

	if err := os.WriteFile("supported-configurations.json", jsonFile, 0644); err != nil {
		log.Error("config: failed to write supported configuration: %v", err)
		return
	}
}
