package internal

import (
	"fmt"
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

func TestResolveAgentAddr(t *testing.T) {
	for _, tt := range []struct {
		in, envHost, envPort, out string
	}{
		{"host", "", "", fmt.Sprintf("host:%s", defaultPort)},
		{"www.my-address.com", "", "", fmt.Sprintf("www.my-address.com:%s", defaultPort)},
		{"localhost", "", "", fmt.Sprintf("localhost:%s", defaultPort)},
		{":1111", "", "", fmt.Sprintf("%s:1111", defaultHostname)},
		{"", "", "", defaultAddress},
		{"custom:1234", "", "", "custom:1234"},
		{"", "", "", defaultAddress},
		{"", "ip.local", "", fmt.Sprintf("ip.local:%s", defaultPort)},
		{"", "", "1234", fmt.Sprintf("%s:1234", defaultHostname)},
		{"", "ip.local", "1234", "ip.local:1234"},
		{"ip.other", "ip.local", "", fmt.Sprintf("ip.local:%s", defaultPort)},
		{"ip.other:1234", "ip.local", "", "ip.local:1234"},
		{":8888", "", "1234", fmt.Sprintf("%s:1234", defaultHostname)},
		{"ip.other:8888", "", "1234", "ip.other:1234"},
		{"ip.other", "ip.local", "1234", "ip.local:1234"},
		{"ip.other:8888", "ip.local", "1234", "ip.local:1234"},
	} {
		t.Run("", func(t *testing.T) {
			if tt.envHost != "" {
				os.Setenv("DD_AGENT_HOST", tt.envHost)
				defer os.Unsetenv("DD_AGENT_HOST")
			}
			if tt.envPort != "" {
				os.Setenv("DD_TRACE_AGENT_PORT", tt.envPort)
				defer os.Unsetenv("DD_TRACE_AGENT_PORT")
			}
			assert.Equal(t, ResolveAgentAddr(tt.in), tt.out)
		})
	}
}
