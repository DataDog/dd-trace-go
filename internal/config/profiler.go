// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import "time"

func (c *Config) ProfilingDelta() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingDelta
}

func (c *Config) ProfilingEndpointCountEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingEndpointCountEnabled
}

func (c *Config) ProfilingDebugCompressionSettings() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingDebugCompressionSettings
}

func (c *Config) ProfilingExecutionTraceConfig() (bool, time.Duration, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingExecutionTraceEnabled, c.profilingExecutionTracePeriod, c.profilingExecutionTraceLimitBytes
}

func (c *Config) ProfilingEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingEnabled
}

func (c *Config) ProfilingAutoEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingAutoEnabled
}

func (c *Config) ProfilingUploadTimeout() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingUploadTimeout
}

func (c *Config) ProfilingAgentless() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingAgentless
}

func (c *Config) ProfilingFlushOnExit() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingFlushOnExit
}

func (c *Config) ProfilingURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingURL
}

func (c *Config) ProfilingOutputDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilingOutputDir
}
