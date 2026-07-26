// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

func (c *Config) OTelExporterOTLPEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPEndpoint
}

func (c *Config) OTelExporterOTLPHeaders() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPHeaders
}

func (c *Config) OTelExporterOTLPProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPProtocol
}

func (c *Config) OTelExporterOTLPTimeout() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPTimeout
}

func (c *Config) OTelExporterOTLPLogsEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPLogsEndpoint
}

func (c *Config) OTelExporterOTLPLogsHeaders() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPLogsHeaders
}

func (c *Config) OTelExporterOTLPLogsProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPLogsProtocol
}

func (c *Config) OTelExporterOTLPLogsTimeout() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPLogsTimeout
}

func (c *Config) OTelBLRPConfig() (maxQueueSize, scheduleDelay, exportTimeout, maxExportBatchSize string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelBLRPMaxQueueSize, c.otelBLRPScheduleDelay, c.otelBLRPExportTimeout, c.otelBLRPMaxExportBatchSize
}

func (c *Config) OTelResourceAttributes() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelResourceAttributes
}

func (c *Config) OTelServiceName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelServiceName
}

func (c *Config) OTelMetricsExporter() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelMetricsExporter
}

func (c *Config) OTelExporterOTLPMetricsEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPMetricsEndpoint
}

func (c *Config) OTelExporterOTLPMetricsHeaders() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPMetricsHeaders
}

func (c *Config) OTelExporterOTLPMetricsProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPMetricsProtocol
}

func (c *Config) OTelExporterOTLPMetricsTimeout() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPMetricsTimeout
}

func (c *Config) OTelExporterOTLPMetricsTemporalityPreference() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelExporterOTLPMetricsTemporalityPreference
}
