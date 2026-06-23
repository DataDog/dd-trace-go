// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal"
)

func TestResolveOTLPTraceURL(t *testing.T) {
	httpAgent := &url.URL{Scheme: "http", Host: "myhost:8126"}
	defaultWithAgent := "http://myhost:4318/v1/traces"
	defaultLocalhost := "http://localhost:4318/v1/traces"

	t.Run("valid http endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "http://traces-collector:4318/v1/traces")
		assert.Equal(t, "http://traces-collector:4318/v1/traces", got)
	})

	t.Run("valid https endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "https://traces-collector:4318/v1/traces")
		assert.Equal(t, "https://traces-collector:4318/v1/traces", got)
	})

	t.Run("unsupported scheme falls back to default", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "grpc://traces-collector:4317")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("missing scheme falls back to default", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "traces-collector:4318/v1/traces")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("default uses agent host with OTLP port", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("default with nil agent URL uses localhost", func(t *testing.T) {
		got := resolveOTLPTraceURL(nil, "")
		assert.Equal(t, defaultLocalhost, got)
	})

	t.Run("default with unix socket agent uses localhost", func(t *testing.T) {
		unixAgent := &url.URL{Scheme: "unix", Path: "/var/run/datadog/apm.socket"}
		got := resolveOTLPTraceURL(unixAgent, "")
		assert.Equal(t, defaultLocalhost, got)
	})
}

func TestDetectUDSURL(t *testing.T) {
	old := internal.DefaultTraceAgentUDSPath
	t.Cleanup(func() { internal.DefaultTraceAgentUDSPath = old })

	t.Run("empty path returns nil", func(t *testing.T) {
		internal.DefaultTraceAgentUDSPath = ""
		assert.Nil(t, detectUDSURL())
	})

	t.Run("nonexistent path still returns URL", func(t *testing.T) {
		internal.DefaultTraceAgentUDSPath = "/nonexistent/path/apm.socket"
		u := detectUDSURL()
		assert.NotNil(t, u)
		assert.Equal(t, URLSchemeUnix, u.Scheme)
		assert.Equal(t, "/nonexistent/path/apm.socket", u.Path)
	})

	t.Run("existing path returns URL with correct scheme and path", func(t *testing.T) {
		f, err := os.CreateTemp("", "apm.socket.*")
		assert.NoError(t, err)
		f.Close()
		t.Cleanup(func() { os.Remove(f.Name()) })

		internal.DefaultTraceAgentUDSPath = f.Name()
		u := detectUDSURL()
		assert.NotNil(t, u)
		assert.Equal(t, URLSchemeUnix, u.Scheme)
		assert.Equal(t, f.Name(), u.Path)
	})
}

func TestValidateSendRetries(t *testing.T) {
	tests := []struct {
		name    string
		retries int
		want    bool
	}{
		{"negative rejected", -1, false},
		{"zero accepted", 0, true},
		{"positive accepted", 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validateSendRetries(tt.retries))
		})
	}
}
