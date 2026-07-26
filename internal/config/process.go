// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import "net/url"

func (c *Config) ExternalEnvironment() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.externalEnvironment
}

func (c *Config) RawGlobalTags() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawGlobalTags
}

func (c *Config) RawServiceName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawServiceName
}

func (c *Config) RawEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawEnv
}

func (c *Config) RawVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawVersion
}

func (c *Config) RawSite() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawSite
}

func (c *Config) RawAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawAPIKey
}

func (c *Config) RawSpanAttributeSchema() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawSpanAttributeSchema
}

func (c *Config) RawTraceAgentURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawTraceAgentURL
}

func (c *Config) RawAgentHost() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawAgentHost
}

func (c *Config) EnvAgentURL() *url.URL {
	c.mu.RLock()
	agentURL := c.rawTraceAgentURL
	agentHost := c.rawAgentHost
	agentPort := c.rawTraceAgentPort
	c.mu.RUnlock()
	return resolveAgentURLWithoutWarnings(agentURL, agentHost, agentPort)
}

func (c *Config) ConfiguredHostname() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configuredHostname
}

func (c *Config) ProcessTagsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.processTagsEnabled
}

func (c *Config) RemoveIntegrationServiceNames() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.removeIntegrationServiceNames
}

func (c *Config) RemoteConfigTUFRoot() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteConfigTUFRoot
}

func (c *Config) RemoteConfigPollIntervalSeconds() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteConfigPollIntervalSeconds
}

func (c *Config) RemoteConfigEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteConfigEnabled
}

func (c *Config) TelemetryDebug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryDebug
}

func (c *Config) TelemetryHeartbeatInterval() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryHeartbeatInterval, c.telemetryHeartbeatIntervalSet
}

func (c *Config) TelemetryDependencyCollectionEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryDependencyCollectionEnabled
}

func (c *Config) TelemetryMetricsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryMetricsEnabled
}

func (c *Config) TelemetryLogCollectionEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryLogCollectionEnabled
}

func (c *Config) TelemetryExtendedHeartbeatInterval() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryExtendedHeartbeatInterval, c.telemetryExtendedHeartbeatIntervalSet
}

func (c *Config) FlagEvaluationCountsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.flagEvaluationCountsEnabled
}

func (c *Config) FlaggingProviderSpanEnrichmentEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.flaggingProviderSpanEnrichmentEnabled
}

func (c *Config) ExperimentalFlaggingProviderEnabledFromEnv() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.experimentalFlaggingProviderEnabledEnv
}
