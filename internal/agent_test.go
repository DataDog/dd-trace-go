package internal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentURLFromEnv(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "", v)
	})

	t.Run("http", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "http://custom:1234")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "http://custom:1234", v)
	})

	t.Run("https", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "https://custom:1234", v)
	})

	t.Run("unix", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "unix:///path/to/custom.socket")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, true, ok)
		assert.Equal(t, "/path/to/custom.socket", v)
	})

	t.Run("unix-path", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "unix://")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "", v)
	})

	t.Run("protocol", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "bad://custom:1234")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "", v)
	})

	t.Run("invalid", func(t *testing.T) {
		os.Setenv("DD_TRACE_AGENT_URL", "http://localhost%+o:8126")
		defer os.Unsetenv("DD_TRACE_AGENT_URL")
		v, ok := AgentURLFromEnv()
		assert.Equal(t, false, ok)
		assert.Equal(t, "", v)
	})
}
