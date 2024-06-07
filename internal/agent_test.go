// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentURLFromEnv(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		want  string
	}{
		"empty": {input: "", want: "http://localhost:8126"},
		// The next two are invalid, in which case we should fall back to the defaults
		"protocol": {input: "bad://custom:1234", want: "http://localhost:8126"},
		"invalid":  {input: "http://localhost%+o:8126", want: "http://localhost:8126"},
		"http":     {input: "http://custom:1234", want: "http://custom:1234"},
		"https":    {input: "https://custom:1234", want: "https://custom:1234"},
		"unix":     {input: "unix:///path/to/custom.socket", want: "unix:///path/to/custom.socket"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", tc.input)
			url := AgentURLFromEnv()
			assert.Equal(t, tc.want, url.String())
		})
	}
}

func TestAgentURLPriorityOrder(t *testing.T) {
	makeTestUDS := func(t *testing.T) string {
		// NB: We don't try to connect to this, we just check that a
		// path exists
		s := t.TempDir()
		old := DefaultTraceAgentUDSPath
		DefaultTraceAgentUDSPath = s
		t.Cleanup(func() { DefaultTraceAgentUDSPath = old })
		return s
	}

	t.Run("DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://foo:1234")
		t.Setenv("DD_AGENT_HOST", "bar")
		t.Setenv("DD_TRACE_AGENT_PORT", "5678")
		_ = makeTestUDS(t)
		url := AgentURLFromEnv()
		assert.Equal(t, "https", url.Scheme)
		assert.Equal(t, "foo:1234", url.Host)
	})

	t.Run("DD_AGENT_HOST-and-DD_TRACE_AGENT_PORT", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "bar")
		t.Setenv("DD_TRACE_AGENT_PORT", "5678")
		_ = makeTestUDS(t)
		url := AgentURLFromEnv()
		assert.Equal(t, "http", url.Scheme)
		assert.Equal(t, "bar:5678", url.Host)
	})

	t.Run("DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "bar")
		_ = makeTestUDS(t)
		url := AgentURLFromEnv()
		assert.Equal(t, "http", url.Scheme)
		assert.Equal(t, "bar:8126", url.Host)
	})

	t.Run("DD_TRACE_AGENT_PORT", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PORT", "5678")
		_ = makeTestUDS(t)
		url := AgentURLFromEnv()
		assert.Equal(t, "http", url.Scheme)
		assert.Equal(t, "localhost:5678", url.Host)
	})

	t.Run("UDS", func(t *testing.T) {
		uds := makeTestUDS(t)
		url := AgentURLFromEnv()
		assert.Equal(t, "unix", url.Scheme)
		assert.Equal(t, "", url.Host)
		assert.Equal(t, uds, url.Path)
	})

	t.Run("nothing", func(t *testing.T) {
		url := AgentURLFromEnv()
		assert.Equal(t, "http", url.Scheme)
		assert.Equal(t, "localhost:8126", url.Host)
	})
}
