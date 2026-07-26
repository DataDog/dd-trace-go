// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

func (c *Config) AppSecEnabled() (bool, Origin) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecEnabled, c.appSecEnabledOrigin
}

func (c *Config) APISecurityEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityEnabled
}

func (c *Config) APISecurityDownstreamBodyAnalysisSampleRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityDownstreamBodyAnalysisSampleRate
}

func (c *Config) APISecurityMaxDownstreamRequestBodyAnalysis() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityMaxDownstreamRequestBodyAnalysis
}

func (c *Config) APISecurityProxySampleRate() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityProxySampleRate
}

func (c *Config) APISecuritySampleDelay() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecuritySampleDelay
}

func (c *Config) APISecurityRequestSampleRate() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityRequestSampleRate
}

func (c *Config) AppSecRASPEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecRASPEnabled
}

func (c *Config) AppSecWAFTimeout() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecWAFTimeout
}

func (c *Config) AppSecTraceRateLimit() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecTraceRateLimit
}

func (c *Config) AppSecRules() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecRules, c.appSecRulesSet
}

func (c *Config) TraceClientIPHeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceClientIPHeader
}

func (c *Config) AppSecStackTraceConfig() (bool, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecStackTraceEnabled, c.appSecMaxStackTraceDepth
}
