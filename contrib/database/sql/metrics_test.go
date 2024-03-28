// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func (cfg *config) applyTags() {
	cfg.serviceName = "my-svc"
	cfg.tags = make(map[string]interface{})
	cfg.tags["tag"] = "value"
}

func setGlobalCfgTags() {
	globalconfig.SetStatsTags([]string{"globaltag:globalvalue"})
}

func resetGlobalConfig() {
	globalconfig.SetStatsTags([]string{})
}

func TestDefaultStatsTags(t *testing.T) {
	cfg := new(config)
	tags := statsTags(cfg)
	assert.Len(t, tags, 0)
}

func TestStatsTagsCfg(t *testing.T) {
	cfg := new(config)
	cfg.applyTags()
	tags := statsTags(cfg)
	assert.Len(t, tags, 2)
	assert.Contains(t, tags, "service:my-svc")
	assert.Contains(t, tags, "tag:value")
}

func TestStatsTagsGlobalconfig(t *testing.T) {
	cfg := new(config)
	setGlobalCfgTags()
	tags := statsTags(cfg)
	assert.Len(t, tags, 1)
	assert.Contains(t, tags, "globaltag:globalvalue")
	resetGlobalConfig()
}

func TestStatsTagsBoth(t *testing.T) {
	cfg := new(config)
	cfg.applyTags()
	setGlobalCfgTags()
	tags := statsTags(cfg)
	assert.Len(t, tags, 3)
	assert.Contains(t, tags, "globaltag:globalvalue")
	assert.Contains(t, tags, "service:my-svc")
	assert.Contains(t, tags, "tag:value")
	resetGlobalConfig()
}
