// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

func (c *Config) AppSecEnabled() (bool, Origin, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecEnabled, c.appSecEnabledOrigin, c.appSecEnabledErr
}

func (c *Config) AppSecSCAEnabledError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecSCAEnabledErr
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

func (c *Config) APISecuritySampleDelay() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecuritySampleDelay, c.apiSecuritySampleDelaySet
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

func (c *Config) AppSecObfuscatorRegexps() (key string, keySet bool, value string, valueSet bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appSecObfuscatorKeyRegexp, c.appSecObfuscatorKeyRegexpSet,
		c.appSecObfuscatorValueRegexp, c.appSecObfuscatorValueRegexpSet
}

func (c *Config) TraceClientIPHeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceClientIPHeader
}
