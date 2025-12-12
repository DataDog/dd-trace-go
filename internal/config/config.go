// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"net/url"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
)

var (
	useFreshConfig bool
	instance       *Config
	// mu protects instance and useFreshConfig
	mu sync.Mutex
)

// Origin represents where a configuration value came from.
// Re-exported so callers don't need to import internal/telemetry.
type Origin = telemetry.Origin

// Re-exported origin constants for common configuration sources
const (
	OriginCode       = telemetry.OriginCode
	OriginCalculated = telemetry.OriginCalculated
)

// Config represents global configuration properties.
// Config instances should be obtained via Get() which always returns a non-nil value.
// Methods on Config assume a non-nil receiver and will panic if called on nil.
type Config struct {
	mu sync.RWMutex
	// Config fields are protected by the mutex.
	agentURL *url.URL
	debug    bool
	// logStartup, when true, causes various startup info to be written when the tracer starts.
	logStartup                 bool
	serviceName                string
	version                    string
	env                        string
	serviceMappings            map[string]string
	hostname                   string
	runtimeMetrics             bool
	runtimeMetricsV2           bool
	profilerHotspots           bool
	profilerEndpoints          bool
	spanAttributeSchemaVersion int
	peerServiceDefaultsEnabled bool
	peerServiceMappings        map[string]string
	debugAbandonedSpans        bool
	spanTimeout                time.Duration
	partialFlushMinSpans       int
	// partialFlushEnabled specifices whether the tracer should enable partial flushing. Value
	// from DD_TRACE_PARTIAL_FLUSH_ENABLED, default false.
	partialFlushEnabled           bool
	statsComputationEnabled       bool
	dataStreamsMonitoringEnabled  bool
	dynamicInstrumentationEnabled bool
	// globalSampleRate holds the sample rate for the tracer.
	globalSampleRate      float64
	ciVisibilityEnabled   bool
	ciVisibilityAgentless bool
	logDirectory          string
	// traceRateLimitPerSecond specifies the rate limit for traces.
	traceRateLimitPerSecond float64
	// logToStdout, if true, indicates we should log all traces to the standard output
	logToStdout bool
	// isLambdaFunction, if true, indicates we are in a lambda function
	isLambdaFunction bool
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	cfg := new(Config)

	// TODO: Use defaults from config json instead of hardcoding them here
	cfg.agentURL = provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "http", Host: "localhost:8126"})
	cfg.debug = provider.getBool("DD_TRACE_DEBUG", false)
	cfg.logStartup = provider.getBool("DD_TRACE_STARTUP_LOGS", true)
	cfg.serviceName = provider.getString("DD_SERVICE", "")
	cfg.version = provider.getString("DD_VERSION", "")
	cfg.env = provider.getString("DD_ENV", "")
	cfg.serviceMappings = provider.getMap("DD_SERVICE_MAPPING", nil)
	cfg.hostname = provider.getString("DD_TRACE_SOURCE_HOSTNAME", "")
	cfg.runtimeMetrics = provider.getBool("DD_RUNTIME_METRICS_ENABLED", false)
	cfg.runtimeMetricsV2 = provider.getBool("DD_RUNTIME_METRICS_V2_ENABLED", true)
	cfg.profilerHotspots = provider.getBool("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", true)
	cfg.profilerEndpoints = provider.getBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", true)
	cfg.spanAttributeSchemaVersion = provider.getInt("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", 0)
	cfg.peerServiceDefaultsEnabled = provider.getBool("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
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
	cfg.traceRateLimitPerSecond = provider.getFloatWithValidator("DD_TRACE_RATE_LIMIT", DefaultRateLimit, validateRateLimit)
	cfg.globalSampleRate = provider.getFloatWithValidator("DD_TRACE_SAMPLE_RATE", math.NaN(), validateSampleRate)

	// AWS_LAMBDA_FUNCTION_NAME being set indicates that we're running in an AWS Lambda environment.
	// See: https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html
	// TODO: Is it possible that we can just use `v != ""` to configure one setting, `lambdaMode` instead
	if v, ok := env.Lookup("AWS_LAMBDA_FUNCTION_NAME"); ok {
		cfg.logToStdout = true
		if v != "" {
			cfg.isLambdaFunction = true
		}
	}

	return cfg
}

// Get returns the global configuration singleton.
// This function is thread-safe and can be called from multiple goroutines concurrently.
// The configuration is lazily initialized on first access using sync.Once, ensuring
// loadConfig() is called exactly once even under concurrent access.
func Get() *Config {
	mu.Lock()
	defer mu.Unlock()
	if useFreshConfig || instance == nil {
		instance = loadConfig()
	}

	return instance
}

func SetUseFreshConfig(use bool) {
	mu.Lock()
	defer mu.Unlock()
	useFreshConfig = use
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

func (c *Config) ProfilerHotspotsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilerHotspots
}

func (c *Config) SetProfilerHotspotsEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profilerHotspots = enabled
	telemetry.RegisterAppConfig(traceprof.CodeHotspotsEnvVar, enabled, origin)
}
func (c *Config) RuntimeMetricsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetrics
}

func (c *Config) SetRuntimeMetricsEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runtimeMetrics = enabled
	telemetry.RegisterAppConfig("DD_RUNTIME_METRICS_ENABLED", enabled, origin)
}

func (c *Config) RuntimeMetricsV2Enabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsV2
}

func (c *Config) SetRuntimeMetricsV2Enabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runtimeMetricsV2 = enabled
	telemetry.RegisterAppConfig("DD_RUNTIME_METRICS_V2_ENABLED", enabled, origin)
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

func (c *Config) LogToStdout() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logToStdout
}

func (c *Config) SetLogToStdout(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logToStdout = enabled
	// Do not report telemetry because this is not a user-configurable option
}

func (c *Config) IsLambdaFunction() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isLambdaFunction
}

func (c *Config) SetIsLambdaFunction(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLambdaFunction = enabled
	// Do not report telemetry because this is not a user-configurable option
}

func (c *Config) GlobalSampleRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.globalSampleRate
}

func (c *Config) SetGlobalSampleRate(rate float64, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.globalSampleRate = rate
	telemetry.RegisterAppConfig("DD_TRACE_SAMPLE_RATE", rate, origin)
}

func (c *Config) TraceRateLimitPerSecond() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceRateLimitPerSecond
}

func (c *Config) SetTraceRateLimitPerSecond(rate float64, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.traceRateLimitPerSecond = rate
	telemetry.RegisterAppConfig("DD_TRACE_RATE_LIMIT", rate, origin)
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
