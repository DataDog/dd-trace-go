// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

import (
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"gopkg.in/yaml.v3"
)

const (
	localFilePath   = "/etc/datadog-agent/application_monitoring.yaml"
	managedFilePath = "/etc/datadog-agent/managed/datadog-agent/stable/application_monitoring.yaml"

	// defaultMaxFileSize defines the default maximum size in bytes for stable config files
	defaultMaxFileSize int64 = 256 * 1024 // 256 KB
)

// LocalConfig holds the configuration loaded from the user-managed file.
var LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig)

// ManagedConfig holds the configuration loaded from the fleet-managed file.
var ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig)

// fileSizeLimit determines the actual maximum size in bytes for stable config files, determined by DD_TRACE_STABLE_CONFIG_FILE_MAX_SIZE if set, else defaults to defaultMaxFileSize
var fileSizeLimit = getFileSizeLimit()

// stableConfigSource represents a source of stable configuration loaded from a file.
type stableConfigSource struct {
	filePath string           // Path to the configuration file.
	origin   telemetry.Origin // Origin identifier for telemetry.
	config   *stableConfig    // Parsed stable configuration.
}

func (s *stableConfigSource) Get(key string) string {
	return s.config.get(key)
}

// newStableConfigSource initializes a new stableConfigSource from the given file.
func newStableConfigSource(filePath string, origin telemetry.Origin) *stableConfigSource {
	return &stableConfigSource{
		filePath: filePath,
		origin:   origin,
		config:   parseFile(filePath),
	}
}

func getFileSizeLimit() int64 {
	if v, ok := os.LookupEnv("DD_TRACE_STABLE_CONFIG_FILE_MAX_SIZE"); ok {
		if vv, err := strconv.ParseInt(v, 10, 64); err == nil {
			return vv
		} else {
			log.Debug("Error converting DD_TRACE_STABLE_CONFIG_FILE_MAX_SIZE value %s to int64; using defaults instead", v)
		}
	}
	return defaultMaxFileSize
}

// ParseFile reads and parses the config file at the given path.
// Returns an empty config if the file doesn't exist or is invalid.
func parseFile(filePath string) *stableConfig {
	info, err := os.Stat(filePath)
	if err != nil {
		// It's expected that the stable config file may not exist; its absence is not an error.
		if !os.IsNotExist(err) {
			log.Warn("Failed to stat stable config file %s, dropping: %v", filePath, err)
		}
		return emptyStableConfig()
	}

	if info.Size() > fileSizeLimit {
		log.Warn("Stable config file %s exceeds size limit (%d bytes > %d bytes), dropping",
			filePath, info.Size(), fileSizeLimit)
		return emptyStableConfig()
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		// It's expected that the stable config file may not exist; its absence is not an error.
		if !os.IsNotExist(err) {
			log.Warn("Failed to read stable config file %s, dropping: %v", filePath, err)
		}
		return emptyStableConfig()
	}

	return fileContentsToConfig(data, filePath)
}

// fileContentsToConfig parses YAML data into a stableConfig struct.
// Returns an empty config if parsing fails or the data is malformed.
func fileContentsToConfig(data []byte, fileName string) *stableConfig {
	scfg := &stableConfig{}
	err := yaml.Unmarshal(data, scfg)
	if err != nil {
		log.Warn("Parsing stable config file" + fileName + "failed due to error, dropping: " + err.Error())
		return emptyStableConfig()
	}
	if scfg.Config == nil {
		scfg.Config = make(map[string]string, 0)
	}
	if scfg.ID == 0 {
		scfg.ID = -1
	}
	return scfg
}
