// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveBLRPMaxQueueSize(t *testing.T) {
	t.Run("defaults to 2048", func(t *testing.T) {
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("uses OTEL_BLRP_MAX_QUEUE_SIZE", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "4096")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 4096, size)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "invalid")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on zero", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "0")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on negative", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "-100")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})
}
