// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"gopkg.in/yaml.v3"
)

const (
	localFilePath   = "/etc/datadog-agent/application_monitoring.yaml"
	managedFilePath = "/etc/datadog-agent/managed/datadog-agent/stable/application_monitoring.yaml"
	maxFileSize     = 4 * 1024 // 4 KB. Was determined based on the size of bigValidYaml (see: stableconfigsource_test.go)
)

var LocalConfig *stableConfigSource = &stableConfigSource{
	filePath: localFilePath,
	origin:   telemetry.OriginLocalStableConfig,
	config:   ParseFile(localFilePath),
}

var ManagedConfig *stableConfigSource = &stableConfigSource{
	filePath: managedFilePath,
	origin:   telemetry.OriginManagedStableConfig,
	config:   ParseFile(managedFilePath),
}

type stableConfigSource struct {
	filePath string
	origin   telemetry.Origin
	config   *stableConfig
}

func (s *stableConfigSource) Get(key string) string {
	return s.config.get(key)
}

func ParseFile(filePath string) *stableConfig {
	// check file size limits
	info, err := os.Stat(filePath)
	if err != nil {
		// There are many valid cases where stable config file won't exist
		if err != os.ErrNotExist {
			// log about it
		}
		return emptyStableConfig()
	}

	if info.Size() > maxFileSize {
		log.Warn("Stable config file %s exceeds size limit (%d bytes > %d bytes), dropping", filePath, info.Size(), maxFileSize)
		return emptyStableConfig()
	}

	// read file, parse contents
	data, err := os.ReadFile(filePath)
	if err == nil {
		return fileContentsToConfig(data, filePath)
	}
	// There are many valid cases where stable config file won't exist
	if err != os.ErrNotExist {
		// log about it
	}
	return emptyStableConfig()
}

func fileContentsToConfig(data []byte, fileName string) *stableConfig {
	scfg := &stableConfig{}
	err := yaml.Unmarshal(data, scfg)
	if err != nil {
		log.Warn("Parsing stable config file" + fileName + "failed due to error: " + err.Error())
		return emptyStableConfig()
	}
	if scfg.Config == nil {
		scfg.Config = make(map[string]string, 0)
	}
	if scfg.Id == 0 {
		scfg.Id = -1
	}
	return scfg
}
