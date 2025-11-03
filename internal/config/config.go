// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"sync"
	"time"
)

var (
	globalConfig *Config
	configOnce   sync.Once
)

// Config represents global configuration properties.
type Config struct {
	// AgentURL is the URL of the Datadog agent.
	AgentURL *url.URL `json:"DD_AGENT_URL"`

	// Debug enables debug logging.
	Debug bool `json:"DD_TRACE_DEBUG"` // has trace in the name, but impacts all products?

	LogStartup bool `json:"DD_TRACE_STARTUP_LOGS"`

	ServiceName string `json:"DD_SERVICE"`

	Version string `json:"DD_VERSION"`

	Env string `json:"DD_ENV"`

	ServiceMappings map[string]string `json:"DD_SERVICE_MAPPING"`

	Hostname string `json:"DD_TRACE_SOURCE_HOSTNAME"`

	RuntimeMetrics bool `json:"DD_RUNTIME_METRICS_ENABLED"`

	RuntimeMetricsV2 bool `json:"DD_RUNTIME_METRICS_V2_ENABLED"`

	ProfilerHotspots bool `json:"DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED"`

	ProfilerEndpoints bool `json:"DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"`

	SpanAttributeSchemaVersion int `json:"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA"`

	PeerServiceDefaultsEnabled bool `json:"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED"`

	PeerServiceMappings map[string]string `json:"DD_TRACE_PEER_SERVICE_MAPPING"`

	DebugAbandonedSpans bool `json:"DD_TRACE_DEBUG_ABANDONED_SPANS"`

	SpanTimeout time.Duration `json:"DD_TRACE_SPAN_TIMEOUT"`

	PartialFlushMinSpans int `json:"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS"`

	PartialFlushEnabled bool `json:"DD_TRACE_PARTIAL_FLUSH_ENABLED"`

	StatsComputationEnabled bool `json:"DD_TRACE_STATS_COMPUTATION_ENABLED"`

	DataStreamsMonitoringEnabled bool `json:"DD_DATA_STREAMS_ENABLED"`

	DynamicInstrumentationEnabled bool `json:"DD_DYNAMIC_INSTRUMENTATION_ENABLED"`

	GlobalSampleRate float64 `json:"DD_TRACE_SAMPLE_RATE"`

	CIVisibilityEnabled bool `json:"DD_CIVISIBILITY_ENABLED"`

	CIVisibilityAgentless bool `json:"DD_CIVISIBILITY_AGENTLESS_ENABLED"`

	LogDirectory string `json:"DD_TRACE_LOG_DIRECTORY"`

	TraceRateLimitPerSecond float64 `json:"DD_TRACE_RATE_LIMIT"`
}

// loadConfig initializes and returns a new Config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through GlobalConfig's sync.Once.
func loadConfig() *Config {
	cfg := new(Config)

	// TODO: Use defaults from config json instead of hardcoding them here
	cfg.AgentURL = provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "http", Host: "localhost:8126"})
	cfg.Debug = provider.getBool("DD_TRACE_DEBUG", false)
	cfg.LogStartup = provider.getBool("DD_TRACE_STARTUP_LOGS", false)
	cfg.ServiceName = provider.getString("DD_SERVICE", "")
	cfg.Version = provider.getString("DD_VERSION", "")
	cfg.Env = provider.getString("DD_ENV", "")
	cfg.ServiceMappings = provider.getMap("DD_SERVICE_MAPPING", nil)
	cfg.Hostname = provider.getString("DD_TRACE_SOURCE_HOSTNAME", "")
	cfg.RuntimeMetrics = provider.getBool("DD_RUNTIME_METRICS_ENABLED", false)
	cfg.RuntimeMetricsV2 = provider.getBool("DD_RUNTIME_METRICS_V2_ENABLED", false)
	cfg.ProfilerHotspots = provider.getBool("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", false)
	cfg.ProfilerEndpoints = provider.getBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", false)
	cfg.SpanAttributeSchemaVersion = provider.getInt("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", 0)
	cfg.PeerServiceDefaultsEnabled = provider.getBool("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
	cfg.PeerServiceMappings = provider.getMap("DD_TRACE_PEER_SERVICE_MAPPING", nil)
	cfg.DebugAbandonedSpans = provider.getBool("DD_TRACE_DEBUG_ABANDONED_SPANS", false)
	cfg.SpanTimeout = provider.getDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)
	cfg.PartialFlushMinSpans = provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
	cfg.PartialFlushEnabled = provider.getBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false)
	cfg.StatsComputationEnabled = provider.getBool("DD_TRACE_STATS_COMPUTATION_ENABLED", false)
	cfg.DataStreamsMonitoringEnabled = provider.getBool("DD_DATA_STREAMS_ENABLED", false)
	cfg.DynamicInstrumentationEnabled = provider.getBool("DD_DYNAMIC_INSTRUMENTATION_ENABLED", false)
	cfg.GlobalSampleRate = provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0)
	cfg.CIVisibilityEnabled = provider.getBool("DD_CIVISIBILITY_ENABLED", false)
	cfg.CIVisibilityAgentless = provider.getBool("DD_CIVISIBILITY_AGENTLESS_ENABLED", false)
	cfg.LogDirectory = provider.getString("DD_TRACE_LOG_DIRECTORY", "")
	cfg.TraceRateLimitPerSecond = provider.getFloat("DD_TRACE_RATE_LIMIT", 0.0)

	return cfg
}

// GlobalConfig returns the global configuration singleton.
// This function is thread-safe and can be called from multiple goroutines concurrently.
// The configuration is lazily initialized on first access using sync.Once, ensuring
// loadConfig() is called exactly once even under concurrent access.
func GlobalConfig() *Config {
	configOnce.Do(func() {
		globalConfig = loadConfig()
	})
	return globalConfig
}

func (c *Config) IsDebugEnabled() bool {
	return c.Debug
}
