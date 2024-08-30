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

func (cfg *config) applyTags2() {
	cfg.serviceName = "my-svc2"
	cfg.tags = make(map[string]interface{})
	cfg.tags["tag"] = "value2"
}

func setGlobalCfgTags() {
	tags := make([]string, 0, 3)
	tags = append(tags, "globaltag:globalvalue")
	globalconfig.SetStatsTags(tags)
}

func resetGlobalConfig() {
	globalconfig.SetStatsTags([]string{})
}

func getGlobalCfgTags() []string {
	return globalconfig.StatsTags()
}

// Test that statsTags(*config) returns tags from the provided *config + whatever is on the globalconfig
func TestStatsTags(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		resetGlobalConfig()
		cfg := new(config)
		tags := statsTags(cfg)
		assert.Len(t, tags, 0)
	})
	t.Run("cfg only", func(t *testing.T) {
		resetGlobalConfig()
		cfg := new(config)
		cfg.applyTags()
		tags := statsTags(cfg)
		assert.Len(t, tags, 2)
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
	})
	t.Run("inherit globalconfig", func(t *testing.T) {
		resetGlobalConfig()
		cfg := new(config)
		setGlobalCfgTags()
		tags := statsTags(cfg)
		assert.Len(t, tags, 1)
		assert.Contains(t, tags, "globaltag:globalvalue")
	})
	t.Run("both", func(t *testing.T) {
		resetGlobalConfig()
		cfg := new(config)
		cfg.applyTags()
		setGlobalCfgTags()
		tags := statsTags(cfg)
		assert.Len(t, tags, 3)
		assert.Contains(t, tags, "globaltag:globalvalue")
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
	})
	t.Run("must not polute globalconfig", func(t *testing.T) {
		resetGlobalConfig()
		setGlobalCfgTags()

		cfg1 := new(config)
		cfg1.applyTags()
		tags1 := statsTags(cfg1)

		cfg2 := new(config)
		cfg2.applyTags2()
		tags2 := statsTags(cfg2)

		assert.Len(t, tags1, 3)
		assert.Contains(t, tags1, "globaltag:globalvalue")
		assert.Contains(t, tags1, "service:my-svc")
		assert.Contains(t, tags1, "tag:value")
		assert.Len(t, tags2, 3)
		assert.Contains(t, tags2, "globaltag:globalvalue")
		assert.Contains(t, tags2, "service:my-svc2")
		assert.Contains(t, tags2, "tag:value2")
	})
	resetGlobalConfig()
}
