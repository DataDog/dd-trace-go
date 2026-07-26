// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"maps"
	"time"
)

func (c *Config) ExternalEnvironment() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.externalEnvironment
}

func (c *Config) GitMetadataEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gitMetadataEnabled
}

func (c *Config) GitRepositoryURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gitRepositoryURL
}

func (c *Config) GitCommitSHA() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gitCommitSHA
}

func (c *Config) RawGlobalTags() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawGlobalTags
}

func (c *Config) GitMetadataTags() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.gitMetadataTags)
}

func (c *Config) GitMetadataTag(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gitMetadataTags[key]
}

func (c *Config) InstrumentationInstallID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instrumentationInstallID
}

func (c *Config) InstrumentationInstallType() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instrumentationInstallType
}

func (c *Config) InstrumentationInstallTime() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instrumentationInstallTime
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

func (c *Config) RemoteConfigPollInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteConfigPollInterval
}

func (c *Config) RemoteConfigEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteConfigEnabled
}

func (c *Config) APISecurityEndpointCollectionMessageLimit() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityEndpointCollectionMessageLimit
}

func (c *Config) TelemetryDebug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetryDebug
}

func (c *Config) TelemetryHeartbeatInterval() (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.telemetryHeartbeatInterval == nil {
		return 0, false
	}
	return *c.telemetryHeartbeatInterval, true
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

func (c *Config) TelemetryExtendedHeartbeatInterval() (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.telemetryExtendedHeartbeatInterval == nil {
		return 0, false
	}
	return *c.telemetryExtendedHeartbeatInterval, true
}

func (c *Config) InstrumentationTelemetryEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instrumentationTelemetryEnabled
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
