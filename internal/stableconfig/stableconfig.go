// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

type stableConfig struct {
	Config map[string]string `yaml:"apm_configuration_default,omitempty"`
	Id     int               `yaml:"config_id,omitempty"`
}

func (s *stableConfig) get(key string) string {
	return s.Config[key]
}

func (s *stableConfig) isEmpty() bool {
	return s.Id == -1 && len(s.Config) == 0
}

func emptyStableConfig() stableConfig {
	return stableConfig{
		Config: make(map[string]string, 0),
		Id:     -1,
	}
}
