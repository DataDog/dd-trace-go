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

func TestWithPoolName(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		cfg := defaultConfig()
		assert.Empty(t, cfg.poolName)
	})
	t.Run("sets pool name", func(t *testing.T) {
		cfg := new(config)
		WithPoolName("my-pool")(cfg)
		assert.Equal(t, "my-pool", cfg.poolName)
	})
}

func TestStatsTags(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		tags := statsTags(&config{})
		assert.Empty(t, tags)
	})
	t.Run("with service name", func(t *testing.T) {
		tags := statsTags(&config{serviceName: "test-service"})
		assert.Contains(t, tags, "service:test-service")
	})
	t.Run("with pool name", func(t *testing.T) {
		tags := statsTags(&config{poolName: "test-pool"})
		assert.Contains(t, tags, "db_client_connection_pool_name:test-pool")
	})
	t.Run("with both service and pool name", func(t *testing.T) {
		tags := statsTags(&config{serviceName: "test-service", poolName: "test-pool"})
		assert.Contains(t, tags, "service:test-service")
		assert.Contains(t, tags, "db_client_connection_pool_name:test-pool")
	})
}
