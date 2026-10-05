// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	// File paths are supported on linux only.
	localFilePath   = "/etc/datadog-agent/application_monitoring.yaml"
	managedFilePath = "/etc/datadog-agent/managed/datadog-agent/stable/application_monitoring.yaml"

	// maxFileSize is the maximum size in bytes for declarative config files (4KB).
	maxFileSize = 4 * 1024
)

type declarativeConfigSource struct {
	filePath    string
	originValue telemetry.Origin
	config      *declarativeConfig
}

func (d *declarativeConfigSource) get(key string) string {
	return d.config.get(normalizeKey(key))
}

func (d *declarativeConfigSource) getID() string {
	return d.config.getID()
}

func (d *declarativeConfigSource) origin() telemetry.Origin {
	return d.originValue
}

func newDeclarativeConfigSource(filePath string, origin telemetry.Origin) *declarativeConfigSource {
	return &declarativeConfigSource{
		filePath:    filePath,
		originValue: origin,
		config:      parseFile(filePath),
	}
}

func parseFile(filePath string) *declarativeConfig {
	info, err := os.Stat(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			// It's expected that the declarative config file may not exist; its absence is not an error.
			log.Warn("Failed to stat declarative config file %q, dropping: %v", filePath, err.Error())
		}
		return emptyDeclarativeConfig()
	}

	if info.Size() > maxFileSize {
		log.Warn("Declarative config file %s exceeds size limit (%d bytes > %d bytes), dropping",
			filePath, info.Size(), maxFileSize)
		return emptyDeclarativeConfig()
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("Failed to read declarative config file %q, dropping: %v", filePath, err.Error())
		}
		return emptyDeclarativeConfig()
	}

	return fileContentsToConfig(data, filePath)
}

func fileContentsToConfig(data []byte, fileName string) *declarativeConfig {
	dc := &declarativeConfig{}
	err := yaml.Unmarshal(data, dc)
	if err != nil {
		log.Warn("Parsing declarative config file %s failed due to error, dropping: %v", fileName, err.Error())
		return emptyDeclarativeConfig()
	}
	if dc.Config == nil {
		dc.Config = make(map[string]string)
	}
	return dc
}
