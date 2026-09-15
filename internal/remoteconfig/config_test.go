// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package remoteconfig

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultClientConfigResolvesAgentConnection(t *testing.T) {
	t.Run("HTTP", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://agent.test:9126")

		cfg := DefaultClientConfig()

		assert.Equal(t, "http://agent.test:9126", cfg.AgentURL)
		require.NotNil(t, cfg.HTTP)
		require.NotNil(t, cfg.HTTP.Transport)
	})

	t.Run("UDS", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix:///tmp/apm.socket")

		cfg := DefaultClientConfig()

		assert.Equal(t, "http://UDS__tmp_apm.socket", cfg.AgentURL)
		transport, ok := cfg.HTTP.Transport.(*http.Transport)
		require.True(t, ok)
		require.NotNil(t, transport.DialContext)
	})
}

func Test_pollIntervalFromEnv(t *testing.T) {
	defaultInterval := time.Second * time.Duration(5.0)
	tests := []struct {
		name  string
		setup func(t *testing.T)
		want  time.Duration
	}{
		{
			name:  "default",
			setup: func(_ *testing.T) {},
			want:  defaultInterval,
		},
		{
			name:  "float",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "0.2") },
			want:  time.Millisecond * 200,
		},
		{
			name:  "integer",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "2") },
			want:  time.Second * 2,
		},
		{
			name:  "negative",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "-1") },
			want:  defaultInterval,
		},
		{
			name:  "zero",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "0") },
			want:  time.Nanosecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			assert.Equal(t, tt.want, pollIntervalFromEnv())
		})
	}
}
