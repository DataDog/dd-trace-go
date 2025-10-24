// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/http"
	"net/url"
	"time"
)

var globalConfig *Config

// Config represents global configuration properties.
type Config struct {
	// AgentURL is the URL of the Datadog agent.
	AgentURL *url.URL `json:"DD_AGENT_URL"`

	// Debug enables debug logging.
	Debug bool `json:"DD_TRACE_DEBUG"` // has trace in the name, but impacts all products?

	// HTTPClient is the HTTP client to use for sending requests to the Datadog agent.
	HTTPClient *http.Client

	FeatureFlags map[string]struct{} `json:"DD_TRACE_FEATURE_FLAGS"`

	LogToStdout bool `json:"DD_TRACE_LOG_TO_STDOUT"`

	SendRetries int `json:"DD_TRACE_SEND_RETRIES"`

	RetryInterval int64 `json:"DD_TRACE_RETRY_INTERVAL"`

	LogStartup bool `json:"DD_TRACE_LOG_STARTUP"`

	ServiceName string `json:"DD_TRACE_SERVICE_NAME"`

	UniversalVersion bool `json:"DD_TRACE_UNIVERSAL_VERSION"`

	Version string `json:"DD_TRACE_VERSION"`

	Env string `json:"DD_TRACE_ENV"`

	ServiceMappings map[string]string `json:"DD_TRACE_SERVICE_MAPPING"`

	HTTPClientTimeout int64 `json:"DD_TRACE_HTTP_CLIENT_TIMEOUT"`

	Hostname string `json:"DD_TRACE_HOSTNAME"`

	RuntimeMetrics bool `json:"DD_TRACE_RUNTIME_METRICS"`

	RuntimeMetricsV2 bool `json:"DD_TRACE_RUNTIME_METRICS_V2"`

	DogstatsdAddr string `json:"DD_TRACE_DOGSTATSD_ADDR"`

	TickChan <-chan time.Time `json:"DD_TRACE_TICK_CHAN"`

	NoDebugStack bool `json:"DD_TRACE_NO_DEBUG_STACK"`

	ProfilerHotspots bool `json:"DD_TRACE_PROFILER_HOTSPOTS"`

	ProfilerEndpoints bool `json:"DD_TRACE_PROFILER_ENDPOINTS"`

	EnableHostnameDetection bool `json:"DD_TRACE_ENABLE_HOSTNAME_DETECTION"`

	SpanAttributeSchemaVersion int `json:"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA_VERSION"`

	PeerServiceDefaultsEnabled bool `json:"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED"`

	PeerServiceMappings map[string]string `json:"DD_TRACE_PEER_SERVICE_MAPPING"`

	DebugAbandonedSpans bool `json:"DD_TRACE_DEBUG_ABANDONED_SPANS"`

	SpanTimeout time.Duration `json:"DD_TRACE_SPAN_TIMEOUT"`

	PartialFlushMinSpans int `json:"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS"`

	PartialFlushEnabled bool `json:"DD_TRACE_PARTIAL_FLUSH_ENABLED"`

	StatsComputationEnabled bool `json:"DD_TRACE_STATS_COMPUTATION_ENABLED"`

	DataStreamsMonitoringEnabled bool `json:"DD_TRACE_DATA_STREAMS_MONITORING_ENABLED"`

	DynamicInstrumentationEnabled bool `json:"DD_TRACE_DYNAMIC_INSTRUMENTATION_ENABLED"`

	GlobalSampleRate float64 `json:"DD_TRACE_GLOBAL_SAMPLE_RATE"`

	CIVisibilityEnabled bool `json:"DD_TRACE_CI_VISIBILITY_ENABLED"`

	CIVisibilityAgentless bool `json:"DD_TRACE_CI_VISIBILITY_AGENTLESS"`

	LogDirectory string `json:"DD_TRACE_LOG_DIRECTORY"`

	TracingAsTransport bool `json:"DD_TRACE_TRACING_AS_TRANSPORT"`

	TraceRateLimitPerSecond float64 `json:"DD_TRACE_TRACE_RATE_LIMIT_PER_SECOND"`

	TraceProtocol float64 `json:"DD_TRACE_TRACE_PROTOCOL"`
}

func loadConfig() *Config {
	cfg := new(Config)

	cfg.AgentURL = provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "http", Host: "localhost:8126"})
	cfg.Debug = provider.getBool("DD_TRACE_DEBUG", false)

	return cfg
}

func GlobalConfig() *Config {
	if globalConfig == nil {
		globalConfig = loadConfig()
	}
	return globalConfig
}

func (c *Config) IsDebugEnabled() bool {
	return c.Debug
}
