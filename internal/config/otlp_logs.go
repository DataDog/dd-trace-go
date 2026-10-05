// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
)

const (
	defaultOTLPLogsTimeout = 30 * time.Second
)

func (c *Config) loadOTLPLogsConfig(p *provider.Provider) {
	genericTimeout := p.GetInt64("OTEL_EXPORTER_OTLP_TIMEOUT", defaultOTLPLogsTimeout.Milliseconds())
	c.otlpLogsTimeout = time.Duration(p.GetInt64("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", genericTimeout)) * time.Millisecond
}

func (c *Config) OTLPLogsTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpLogsTimeout
}
