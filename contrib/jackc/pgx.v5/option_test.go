// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithPoolStats(t *testing.T) {
	t.Run("default off", func(t *testing.T) {
		cfg := defaultConfig()
		assert.False(t, cfg.poolStats)
	})
	t.Run("on", func(t *testing.T) {
		cfg := new(config)
		WithPoolStats()(cfg)
		assert.True(t, cfg.poolStats)
	})
}
