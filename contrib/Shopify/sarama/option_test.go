// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataStreamsActivation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg)
		assert.False(t, cfg.dataStreamsEnabled)
	})
	t.Run("withOption", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg)
		WithDataStreams()(cfg)
		assert.True(t, cfg.dataStreamsEnabled)
	})
	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		cfg := new(config)
		defaults(cfg)
		assert.True(t, cfg.dataStreamsEnabled)
	})
	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		cfg := new(config)
		defaults(cfg)
		WithDataStreams()(cfg)
		assert.True(t, cfg.dataStreamsEnabled)
	})
}

func TestClusterIDConcurrency(t *testing.T) {
	cfg := new(config)
	defaults(cfg)

	const numReaders = 10
	const numIterations = 1000

	var wg sync.WaitGroup

	wg.Go(func() {
		for range numIterations {
			cfg.SetClusterID(fmt.Sprintf("cluster-%d", 0))
		}
	})

	for range numReaders {
		wg.Go(func() {
			for range numIterations {
				id := cfg.ClusterID()
				// The ID should either be empty (not yet set) or a valid cluster ID
				if id != "" {
					assert.Contains(t, id, "cluster-")
				}
			}
		})
	}

	wg.Wait()

	assert.NotEmpty(t, cfg.ClusterID())
}
