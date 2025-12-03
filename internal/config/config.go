// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
)

var (
	useFreshConfig atomic.Bool
	instance       atomic.Value
)

// Config represents global configuration properties.
// Config instances should be obtained via Get() which always returns a non-nil value.
// Methods on Config assume a non-nil receiver and will panic if called on nil.
type Config struct {
	mu sync.RWMutex
	// Config fields are protected by the mutex.
	agentURL                      *url.URL
	debug                         bool
	logStartup                    bool
	runtimeMetrics                bool
	runtimeMetricsV2              bool
	profilerHotspots              bool
	profilerEndpoints             bool
	logToStdout                   bool
	peerServiceDefaultsEnabled    bool
	debugAbandonedSpans           bool
	partialFlushEnabled           bool
	statsComputationEnabled       bool
	dataStreamsMonitoringEnabled  bool
	dynamicInstrumentationEnabled bool
	ciVisibilityEnabled           bool
	ciVisibilityAgentless         bool
	serviceName                   string
	version                       string
	env                           string
	serviceMappings               map[string]string
	hostname                      string
	spanAttributeSchemaVersion    int
	peerServiceMappings           map[string]string
	spanTimeout                   time.Duration
	partialFlushMinSpans          int
	globalSampleRate              float64
	logDirectory                  string
	traceRateLimitPerSecond       float64
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	cfg := new(Config)

	// TODO: Use defaults from config json instead of hardcoding them here
	cfg.agentURL = provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "http", Host: "localhost:8126"})

	cfg.debug = provider.getBool("DD_TRACE_DEBUG", false)
	cfg.logStartup = provider.getBool("DD_TRACE_STARTUP_LOGS", true)
	cfg.runtimeMetrics = provider.getBool("DD_RUNTIME_METRICS_ENABLED", false)
	cfg.runtimeMetricsV2 = provider.getBool("DD_RUNTIME_METRICS_V2_ENABLED", true)
	cfg.profilerHotspots = provider.getBool(traceprof.CodeHotspotsEnvVar, true)
	cfg.profilerEndpoints = provider.getBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", true)
	cfg.spanAttributeSchemaVersion = provider.getInt("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", 0)
	cfg.peerServiceDefaultsEnabled = provider.getBool("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)

	cfg.serviceName = provider.getString("DD_SERVICE", "")
	cfg.version = provider.getString("DD_VERSION", "")
	cfg.env = provider.getString("DD_ENV", "")
	cfg.serviceMappings = provider.getMap("DD_SERVICE_MAPPING", nil)
	cfg.hostname = provider.getString("DD_TRACE_SOURCE_HOSTNAME", "")
	cfg.peerServiceMappings = provider.getMap("DD_TRACE_PEER_SERVICE_MAPPING", nil)
	cfg.debugAbandonedSpans = provider.getBool("DD_TRACE_DEBUG_ABANDONED_SPANS", false)
	cfg.spanTimeout = provider.getDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)
	cfg.partialFlushMinSpans = provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
	cfg.partialFlushEnabled = provider.getBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false)
	cfg.statsComputationEnabled = provider.getBool("DD_TRACE_STATS_COMPUTATION_ENABLED", false)
	cfg.dataStreamsMonitoringEnabled = provider.getBool("DD_DATA_STREAMS_ENABLED", false)
	cfg.dynamicInstrumentationEnabled = provider.getBool("DD_DYNAMIC_INSTRUMENTATION_ENABLED", false)
	cfg.globalSampleRate = provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0)
	cfg.ciVisibilityEnabled = provider.getBool("DD_CIVISIBILITY_ENABLED", false)
	cfg.ciVisibilityAgentless = provider.getBool("DD_CIVISIBILITY_AGENTLESS_ENABLED", false)
	cfg.logDirectory = provider.getString("DD_TRACE_LOG_DIRECTORY", "")
	cfg.traceRateLimitPerSecond = provider.getFloat("DD_TRACE_RATE_LIMIT", 0.0)

	return cfg
}

// Get returns the global configuration singleton.
// This function is thread-safe and can be called from multiple goroutines concurrently.
// The configuration is lazily initialized on first access using sync.Once, ensuring
// loadConfig() is called exactly once even under concurrent access.
func Get() *Config {
	v := instance.Load()
	if v == nil || useFreshConfig.Load() {
		cfg := loadConfig()
		instance.Store(cfg)
		return cfg
	}
	return v.(*Config)
}

func SetUseFreshConfig(use bool) {
	useFreshConfig.Store(use)
}

func (c *Config) Debug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debug
}

func (c *Config) SetDebug(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debug = enabled
	telemetry.RegisterAppConfig("DD_TRACE_DEBUG", enabled, origin)
}

func (c *Config) LogStartup() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logStartup
}

func (c *Config) SetLogStartup(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logStartup = enabled
	telemetry.RegisterAppConfig("DD_TRACE_STARTUP_LOGS", enabled, origin)
}

func (c *Config) RuntimeMetrics() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetrics
}

func (c *Config) SetRuntimeMetrics(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runtimeMetrics = enabled
	telemetry.RegisterAppConfig("DD_RUNTIME_METRICS_ENABLED", enabled, origin)
}

func (c *Config) RuntimeMetricsV2() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsV2
}

func (c *Config) SetRuntimeMetricsV2(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runtimeMetricsV2 = enabled
	telemetry.RegisterAppConfig("DD_RUNTIME_METRICS_V2_ENABLED", enabled, origin)
}

func (c *Config) ProfilerHotspots() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilerHotspots
}

func (c *Config) SetProfilerHotspots(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profilerHotspots = enabled
	telemetry.RegisterAppConfig("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", enabled, origin)
}

func (c *Config) ProfilerEndpoints() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilerEndpoints
}

func (c *Config) SetProfilerEndpoints(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profilerEndpoints = enabled
	telemetry.RegisterAppConfig("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", enabled, origin)
}

func (c *Config) PeerServiceDefaultsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peerServiceDefaultsEnabled
}

func (c *Config) SetPeerServiceDefaultsEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peerServiceDefaultsEnabled = enabled
	telemetry.RegisterAppConfig("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", enabled, origin)
}

func (c *Config) DebugAbandonedSpans() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debugAbandonedSpans
}

func (c *Config) SetDebugAbandonedSpans(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debugAbandonedSpans = enabled
	telemetry.RegisterAppConfig("DD_TRACE_DEBUG_ABANDONED_SPANS", enabled, origin)
}

func (c *Config) PartialFlushEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.partialFlushEnabled
}

func (c *Config) SetPartialFlushEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partialFlushEnabled = enabled
	telemetry.RegisterAppConfig("DD_TRACE_PARTIAL_FLUSH_ENABLED", enabled, origin)
}

func (c *Config) StatsComputationEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statsComputationEnabled
}

func (c *Config) SetStatsComputationEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsComputationEnabled = enabled
	telemetry.RegisterAppConfig("DD_TRACE_STATS_COMPUTATION_ENABLED", enabled, origin)
}

func (c *Config) DataStreamsMonitoringEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dataStreamsMonitoringEnabled
}

func (c *Config) SetDataStreamsMonitoringEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataStreamsMonitoringEnabled = enabled
	telemetry.RegisterAppConfig("DD_DATA_STREAMS_ENABLED", enabled, origin)
}

func (c *Config) DynamicInstrumentationEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dynamicInstrumentationEnabled
}

func (c *Config) SetDynamicInstrumentationEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dynamicInstrumentationEnabled = enabled
	telemetry.RegisterAppConfig("DD_DYNAMIC_INSTRUMENTATION_ENABLED", enabled, origin)
}

func (c *Config) CiVisibilityEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityEnabled
}

func (c *Config) SetCiVisibilityEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ciVisibilityEnabled = enabled
	telemetry.RegisterAppConfig("DD_CIVISIBILITY_ENABLED", enabled, origin)
}

func (c *Config) CiVisibilityAgentless() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityAgentless
}

func (c *Config) SetCiVisibilityAgentless(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ciVisibilityAgentless = enabled
	telemetry.RegisterAppConfig("DD_CIVISIBILITY_AGENTLESS_ENABLED", enabled, origin)
}

func (c *Config) LogToStdout() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logToStdout
}

func (c *Config) SetLogToStdout(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logToStdout = enabled
	telemetry.RegisterAppConfig("DD_TRACE_LOG_TO_STDOUT", enabled, origin)
}
