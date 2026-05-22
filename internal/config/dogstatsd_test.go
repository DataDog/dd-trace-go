// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// TestDogstatsdAddrResolution exercises the full DogStatsD address
// resolution path through internal/config: env-sourced inputs at loadConfig
// time, an optional user-supplied address via SetDogstatsdAddr, and the
// agent-reported port via ApplyAgentReportedStatsdPort.
func TestDogstatsdAddrResolution(t *testing.T) {
	socketFile, err := os.CreateTemp("", "dsd.socket")
	require.NoError(t, err)
	require.NoError(t, socketFile.Close())
	t.Cleanup(func() { os.RemoveAll(socketFile.Name()) })
	socketPath := socketFile.Name()

	tests := []struct {
		name            string
		userAddr        string
		agentStatsdPort int
		env             map[string]string
		socketPath      string
		expected        string
	}{
		{
			name:     "defaults",
			expected: "localhost:8125",
		},
		{
			name:     "host-env",
			env:      map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1", "DD_AGENT_HOST": "222.222.2.2"},
			expected: "111.111.1.1:8125",
		},
		{
			name:     "port-env",
			env:      map[string]string{"DD_DOGSTATSD_PORT": "8111"},
			expected: "localhost:8111",
		},
		{
			name:     "port-env+agent-host-env",
			env:      map[string]string{"DD_DOGSTATSD_PORT": "8111", "DD_AGENT_HOST": "222.222.2.2"},
			expected: "222.222.2.2:8111",
		},
		{
			name:     "host-env+port-env",
			env:      map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1", "DD_DOGSTATSD_PORT": "8888", "DD_AGENT_HOST": "222.222.2.2"},
			expected: "111.111.1.1:8888",
		},
		{
			name:       "host-env+socket",
			env:        map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1"},
			socketPath: socketPath,
			expected:   "111.111.1.1:8125",
		},
		{
			name:       "port-env+socket",
			env:        map[string]string{"DD_DOGSTATSD_PORT": "8111"},
			socketPath: socketPath,
			expected:   "localhost:8111",
		},
		{
			name:       "socket",
			socketPath: socketPath,
			expected:   "unix://" + socketPath,
		},
		// DD_AGENT_HOST alone should not trigger the env var path;
		// it falls through to auto-discovery.
		{
			name:       "agent-host-env-only+socket",
			env:        map[string]string{"DD_AGENT_HOST": "222.222.2.2"},
			socketPath: socketPath,
			expected:   "unix://" + socketPath,
		},
		{
			name:     "agent-host-env-only",
			env:      map[string]string{"DD_AGENT_HOST": "222.222.2.2"},
			expected: "222.222.2.2:8125",
		},
		{
			name:            "agent-host-env-only+agent-port",
			env:             map[string]string{"DD_AGENT_HOST": "222.222.2.2"},
			agentStatsdPort: 9876,
			expected:        "222.222.2.2:9876",
		},
		// userAddr (priority 1) wins over everything.
		{
			name:     "user-addr",
			userAddr: "custom:9999",
			expected: "custom:9999",
		},
		{
			name:     "user-addr+env",
			userAddr: "custom:9999",
			env:      map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1", "DD_DOGSTATSD_PORT": "8111"},
			expected: "custom:9999",
		},
		{
			name:       "user-addr+socket",
			userAddr:   "custom:9999",
			socketPath: socketPath,
			expected:   "custom:9999",
		},
		{
			name:            "user-addr+agent-port",
			userAddr:        "custom:9999",
			agentStatsdPort: 9876,
			expected:        "custom:9999",
		},
		// Agent-reported port used as fallback when env host is set but no env port.
		{
			name:            "host-env+agent-port",
			env:             map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1"},
			agentStatsdPort: 9876,
			expected:        "111.111.1.1:9876",
		},
		// Env port wins over agent-reported port.
		{
			name:            "host-env+port-env+agent-port",
			env:             map[string]string{"DD_DOGSTATSD_HOST": "111.111.1.1", "DD_DOGSTATSD_PORT": "8111"},
			agentStatsdPort: 9876,
			expected:        "111.111.1.1:8111",
		},
		// Auto-discovery: agent-reported port when no env and no socket.
		{
			name:            "agent-port",
			agentStatsdPort: 9876,
			expected:        "localhost:9876",
		},
		// Auto-discovery: socket wins over agent-reported port.
		{
			name:            "socket+agent-port",
			agentStatsdPort: 9876,
			socketPath:      socketPath,
			expected:        "unix://" + socketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"DD_DOGSTATSD_HOST", "DD_DOGSTATSD_PORT", "DD_AGENT_HOST"} {
				t.Setenv(key, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			restoreSocket := DefaultSocketDSDPath
			DefaultSocketDSDPath = tt.socketPath
			defer func() { DefaultSocketDSDPath = restoreSocket }()

			c := CreateNew()
			if tt.userAddr != "" {
				c.SetDogstatsdAddr(tt.userAddr, telemetry.OriginCode, ProductTracer)
			}
			c.ApplyAgentReportedStatsdPort(tt.agentStatsdPort)
			assert.Equal(t, tt.expected, c.DogstatsdAddr())
		})
	}
}
