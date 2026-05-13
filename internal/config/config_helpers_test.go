// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
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
