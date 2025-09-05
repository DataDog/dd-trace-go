// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment

import (
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal/config"
)

type newCfg struct {
	projectName   string
	tags          map[string]string
	experimentCfg map[string]any
}

func defaultNewCfg(globalCfg *config.Config) *newCfg {
	projectName := ""
	if globalCfg != nil {
		projectName = globalCfg.ProjectName
	}
	return &newCfg{
		projectName:   projectName,
		tags:          nil,
		experimentCfg: nil,
	}
}

type Option func(cfg *newCfg)

func WithProjectName(name string) Option {
	return func(cfg *newCfg) {
		cfg.projectName = name
	}
}

func WithTags(tags map[string]string) Option {
	return func(cfg *newCfg) {
		cfg.tags = tags
	}
}

func WithExperimentConfig(experimentCfg map[string]any) Option {
	return func(cfg *newCfg) {
		cfg.experimentCfg = experimentCfg
	}
}

type runCfg struct {
	maxConcurrency int
	abortOnError   bool
	sampleSize     int
}

func defaultRunCfg() *runCfg {
	return &runCfg{
		maxConcurrency: 0,
		abortOnError:   false,
		sampleSize:     0,
	}
}

type RunOption func(cfg *runCfg)
