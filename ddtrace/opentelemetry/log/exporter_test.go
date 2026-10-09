// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestResolveLogsAgentEndpoint(t *testing.T) {
	cfg := config.CreateNew()
	t.Cleanup(func() { config.CreateNew() })
	for _, tc := range []struct {
		name     string
		scheme   string
		host     string
		port     string
		endpoint string
		insecure bool
	}{
		{name: "http", scheme: "http", host: "trace-agent:8126", port: defaultOTLPHTTPPort, endpoint: "trace-agent:4318", insecure: true},
		{name: "http with https", scheme: "https", host: "trace-agent:8126", port: defaultOTLPHTTPPort, endpoint: "trace-agent:4318"},
		{name: "http with IPv6", scheme: "http", host: "[::1]:8126", port: defaultOTLPHTTPPort, endpoint: "[::1]:4318", insecure: true},
		{name: "grpc", scheme: "http", host: "trace-agent:8126", port: defaultOTLPGRPCPort, endpoint: "trace-agent:4317", insecure: true},
		{name: "grpc with https", scheme: "https", host: "trace-agent:8126", port: defaultOTLPGRPCPort, endpoint: "trace-agent:4317"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg.SetAgentURL(&url.URL{Scheme: tc.scheme, Host: tc.host}, config.OriginCode)
			endpoint, insecure := resolveLogsAgentEndpoint(tc.port)
			assert.Equal(t, tc.endpoint, endpoint)
			assert.Equal(t, tc.insecure, insecure)
		})
	}
}

func TestSanitizeOTLPEndpointEscapedBasePath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "escaped segment", endpoint: "http://collector/tenant%2Fblue", want: "http://collector/tenant%2Fblue/v1/logs"},
		{name: "escaped trailing slash", endpoint: "http://collector/tenant%2F", want: "http://collector/tenant%2F/v1/logs"},
		{name: "escaped segment with trailing slash", endpoint: "http://collector/tenant%2Fblue/", want: "http://collector/tenant%2Fblue/v1/logs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sanitizeOTLPEndpoint(tc.endpoint, true))
		})
	}
}
