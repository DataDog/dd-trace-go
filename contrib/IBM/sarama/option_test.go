// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"fmt"
	"sync"
	"testing"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestWithSaramaConfig(t *testing.T) {
	sc := sarama.NewConfig()
	sc.ClientID = "test-client"

	t.Run("consumer", func(t *testing.T) {
		mc := mocks.NewConsumer(t, nil)
		mc.ExpectConsumePartition("test-topic", 0, sarama.OffsetOldest)
		c := WrapConsumer(mc, WithSaramaConfig(sc))
		wc := c.(*consumer)
		assert.Same(t, sc, wc.cfg.saramaConfig)

		pc, err := c.ConsumePartition("test-topic", 0, sarama.OffsetOldest)
		require.NoError(t, err)
		assert.Same(t, sc, pc.(*partitionConsumer).dispatcher.(*wrappedDispatcher).cfg.saramaConfig)
		require.NoError(t, pc.Close())
		require.NoError(t, c.Close())
	})
	t.Run("consumer group handler", func(t *testing.T) {
		h := WrapConsumerGroupHandler(nil, WithSaramaConfig(sc))
		assert.Same(t, sc, h.(*consumerGroupHandler).cfg.saramaConfig)
	})
	t.Run("producer uses its own configuration", func(t *testing.T) {
		pc := sarama.NewConfig()
		mp := mocks.NewSyncProducer(t, nil)
		p := WrapSyncProducer(pc, mp, WithSaramaConfig(sc))
		assert.Same(t, pc, p.(*syncProducer).cfg.saramaConfig)
		require.NoError(t, p.Close())
	})
}
