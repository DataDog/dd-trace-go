package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

// Test that statsTags(*config) returns tags from the provided *config + whatever is on the globalconfig
func TestStatsTags(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		cfg := new(config)
		tags := statsTags(cfg)
		assert.Len(t, tags, 0)
	})
	t.Run("cfg only", func(t *testing.T) {
		cfg := new(config)
		cfg.serviceName = "my-svc"
		cfg.tags = make(map[string]interface{})
		cfg.tags["tag"] = "value"
		tags := statsTags(cfg)
		assert.Len(t, tags, 2)
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
	})
	t.Run("globalconfig", func(t *testing.T) {
		cfg := new(config)
		globalconfig.SetStatsTags([]string{"globaltag:globalvalue"})
		tags := statsTags(cfg)
		assert.Len(t, tags, 1)
		assert.Contains(t, tags, "globaltag:globalvalue")
		// reset globalconfig
		globalconfig.SetStatsTags([]string{})
	})
	t.Run("both", func(t *testing.T) {
		cfg := new(config)
		globalconfig.SetStatsTags([]string{"globaltag:globalvalue"})
		cfg.serviceName = "my-svc"
		cfg.tags = make(map[string]interface{})
		cfg.tags["tag"] = "value"
		tags := statsTags(cfg)
		assert.Len(t, tags, 3)
		assert.Contains(t, tags, "globaltag:globalvalue")
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
		// reset globalconfig
		globalconfig.SetStatsTags([]string{})
	})
}
