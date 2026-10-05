// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
)

const (
	defaultOTLPLogsTimeout = 30 * time.Second
)

func (c *Config) loadOTLPLogsConfig(p *provider.Provider, genericProtocol, genericEndpoint string) {
	c.otlpLogsProtocol = strings.ToLower(strings.TrimSpace(p.GetString("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", genericProtocol)))
	c.otlpLogsEndpoint = p.GetString("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", genericEndpoint)
	genericTimeout := p.GetInt64("OTEL_EXPORTER_OTLP_TIMEOUT", defaultOTLPLogsTimeout.Milliseconds())
	c.otlpLogsTimeout = time.Duration(p.GetInt64("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", genericTimeout)) * time.Millisecond
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
