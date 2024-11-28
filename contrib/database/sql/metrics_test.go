// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func applyTags(cfg *config) {
	cfg.serviceName = "my-svc"
	cfg.tags = make(map[string]interface{})
	cfg.tags["tag"] = "value"
}

// Test that statsTags(*config) returns tags from the provided *config + whatever is on the globalconfig
func TestStatsTags(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		cfg := new(config)
		tags := cfg.statsdExtraTags()
		assert.Len(t, tags, 0)
	})
	t.Run("add tags from config", func(t *testing.T) {
		cfg := new(config)
		applyTags(cfg)
		tags := cfg.statsdExtraTags()
		assert.Len(t, tags, 2)
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
	})
}
