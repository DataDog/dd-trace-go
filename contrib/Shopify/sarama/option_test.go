package sarama

import (
	"github.com/stretchr/testify/assert"
	"testing"
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
