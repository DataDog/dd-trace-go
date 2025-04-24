// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import "gopkg.in/yaml.v3"

type stableConfig struct {
	Config configAllowList `yaml:"apm_configuration_default,omitempty"`
	Id     int             `yaml:"config_id,omitempty"`
}

type configAllowList map[string]string

var allowlist = map[string]struct{}{
	"DD_APM_TRACING_ENABLED":     {},
	"DD_RUNTIME_METRICS_ENABLED": {},
	"DD_LOGS_INJECTION":          {},
	"DD_PROFILING_ENABLED":       {},
	"DD_DATA_STREAMS_ENABLED":    {},
	"DD_APPSEC_ENABLED":          {},
	// DD_IAST_ENABLED Not currently supported, retain for telemtry?
	"DD_IAST_ENABLED":                    {},
	"DD_DYNAMIC_INSTRUMENTATION_ENABLED": {},
	"DD_DATA_JOBS_ENABLED":               {},
	"DD_APPSEC_SCA_ENABLED":              {},
	"DD_TRACE_DEBUG":                     {},
}

func (l *configAllowList) UnmarshalYaml(value *yaml.Node) error {
	temp := make(map[string]string)
	if err := value.Decode(&temp); err != nil {
		return err
	}

	filtered := make(map[string]string)
	for k, v := range temp {
		if _, ok := allowlist[k]; ok {
			filtered[k] = v
		}
	}

	*l = filtered
	return nil
}

func (s *stableConfig) get(key string) string {
	return s.Config[key]
}

func (s *stableConfig) isEmpty() bool {
	return s.Id == -1 && len(s.Config) == 0
}

func emptyStableConfig() *stableConfig {
	return &stableConfig{
		Config: make(map[string]string, 0),
		Id:     -1,
	}
}
