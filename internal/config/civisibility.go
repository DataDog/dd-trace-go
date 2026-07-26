// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

func (c *Config) CIVisibilityDebugEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityDebug
}

func (c *Config) CIVisibilityEnabledRaw() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityEnabledRaw, c.ciVisibilityEnabledSet
}

func (c *Config) CIVisibilityAgentlessFromEnv() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityAgentlessEnv
}

func (c *Config) CIVisibilityAgentlessURLFromEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityAgentlessURLEnv
}

func (c *Config) CIVisibilityGitUploadEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityGitUploadEnabled
}

func (c *Config) CIVisibilityFlakyRetryEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityFlakyRetryEnabled
}

func (c *Config) CIVisibilityImpactedTestsDetectionEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityImpactedTestsDetectionEnabled
}

func (c *Config) CIVisibilityCodeCoverageReportUploadEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityCodeCoverageReportUploadEnabled
}

func (c *Config) TestManagementEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.testManagementEnabled
}

func (c *Config) TestManagementAttemptToFixRetries() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.testManagementAttemptToFixRetries
}

func (c *Config) CIVisibilitySubtestFeaturesEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilitySubtestFeaturesEnabled
}

func (c *Config) CIVisibilityTotalFlakyRetryCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityTotalFlakyRetryCount
}

func (c *Config) CIVisibilityFlakyRetryCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityFlakyRetryCount
}

func (c *Config) CIVisibilityInternalParallelEFDEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityInternalParallelEFDEnabled
}

func (c *Config) CIVisibilityLogsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityLogsEnabled
}

func (c *Config) TestSessionName() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.testSessionName, c.testSessionNameSet
}

func (c *Config) TestOptimizationEnvironmentDataFile() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.testOptimizationEnvironmentDataFile
}

func (c *Config) PipelineExecutionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pipelineExecutionID
}

func (c *Config) ActionExecutionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actionExecutionID
}

func (c *Config) CodeCoverageFlags() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codeCoverageFlags
}

func (c *Config) CIVisibilityAutoInstrumentationProvider() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityAutoInstrumentationProvider
}
