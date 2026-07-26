// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

func (c *Config) LLMObsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.llmObsEnabled
}

func (c *Config) LLMObsMLApp() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.llmObsMLApp
}

func (c *Config) LLMObsProjectName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.llmObsProjectName
}

func (c *Config) LLMObsAgentlessEnabled() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.llmObsAgentlessEnabled == nil {
		return nil
	}
	value := *c.llmObsAgentlessEnabled
	return &value
}

func (c *Config) APMTracingEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apmTracingEnabled
}

func (c *Config) PropagationStyle() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propagationStyle
}

func (c *Config) TraceID128BitLoggingEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceID128BitLoggingEnabled
}

func (c *Config) DebugSeelogWorkaroundEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debugSeelogWorkaroundEnabled
}

func (c *Config) OTelTracesSamplerArg() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otelTracesSamplerArg
}

func (c *Config) PropagationStyleInjectFromEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propagationStyleInjectEnv
}

func (c *Config) PropagationStyleExtractFromEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propagationStyleExtractEnv
}

func (c *Config) PropagationBehaviorExtractFromEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propagationBehaviorExtractEnv
}

func (c *Config) PropagationExtractFirstFromEnv() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.propagationExtractFirstEnv
}

func (c *Config) APISecurityEndpointCollectionEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiSecurityEndpointCollectionEnabled
}

func (c *Config) GraphQLErrorExtensions() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.graphQLErrorExtensions
}

func (c *Config) PubsubPropagationAsSpanLinks() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pubsubPropagationAsSpanLinks, c.pubsubPropagationAsSpanLinksSet
}

func (c *Config) KafkaAnalyticsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.kafkaAnalyticsEnabled
}

func (c *Config) HTTPQueryStringDisabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.httpQueryStringDisabled
}

func (c *Config) TraceClientIPEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceClientIPEnabled
}

func (c *Config) InferredProxyServicesEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inferredProxyServicesEnabled
}

func (c *Config) ResourceRenamingAlwaysSimplifiedEndpoint() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resourceRenamingAlwaysSimplifiedEndpoint
}

func (c *Config) HTTPServerErrorStatuses() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.httpServerErrorStatuses
}

func (c *Config) ResourceRenamingEnabled() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.resourceRenamingEnabled == nil {
		return nil
	}
	value := *c.resourceRenamingEnabled
	return &value
}

func (c *Config) HTTPBaggageTagKeys() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.httpBaggageTagKeys, c.httpBaggageTagKeysSet
}

func (c *Config) HTTPQueryStringAllowlists() (global, client, server string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.httpQueryStringAllowlist, c.httpClientQueryStringAllowlist, c.httpServerQueryStringAllowlist
}

func (c *Config) HTTPQueryStringObfuscationRegexp() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.httpQueryStringObfuscationRegexp, c.httpQueryStringObfuscationRegexpConfigured
}
