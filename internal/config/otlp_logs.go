// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"cmp"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
)

const (
	defaultOTLPLogsTimeout = 30 * time.Second
)

func (c *Config) loadOTLPLogsConfig(p *provider.Provider, agentHost, genericProtocol, genericEndpoint string) {
	c.otlpLogsProtocol = strings.ToLower(strings.TrimSpace(p.GetString("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", genericProtocol)))
	c.otlpLogsEndpoint = p.GetString("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", genericEndpoint)
	genericTimeout := p.GetInt64("OTEL_EXPORTER_OTLP_TIMEOUT", defaultOTLPLogsTimeout.Milliseconds())
	c.otlpLogsTimeout = time.Duration(p.GetInt64("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", genericTimeout)) * time.Millisecond
	c.otlpLogsAgentHost = cmp.Or(agentHost, internal.DefaultAgentHostname)
}

func (c *Config) OTLPLogsProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpLogsProtocol
}

// OTLPLogsEndpoint returns the configured logs endpoint, or an empty string
// when the exporter should derive its endpoint from the agent configuration.
func (c *Config) OTLPLogsEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpLogsEndpoint
}

func (c *Config) OTLPLogsTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpLogsTimeout
}

// OTLPLogsAgentURL returns the agent URL before the exporter selects its OTLP port.
func (c *Config) OTLPLogsAgentURL() *url.URL {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.agentURL != nil && c.agentURL.Hostname() != "" {
		u := *c.agentURL
		return &u
	}
	return &url.URL{Scheme: URLSchemeHTTP, Host: net.JoinHostPort(c.otlpLogsAgentHost, internal.DefaultTraceAgentPort)}
}
