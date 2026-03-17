// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	configtelemetry "github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
	logStartup  bool
	serviceName string
	version     string
	// env contains the environment that this application will run under.
	env string
	// serviceMappings holds a set of service mappings to dynamically rename services
	serviceMappings map[string]string
	// hostname is automatically assigned from the OS hostname, or from the DD_TRACE_SOURCE_HOSTNAME environment variable or WithHostname() option.
	hostname string
	// hostnameLookupError is the error returned by os.Hostname() if it fails
	hostnameLookupError        error
	runtimeMetrics             bool
	runtimeMetricsV2           bool
	profilerHotspots           bool
	profilerEndpoints          bool
	spanAttributeSchemaVersion int
	peerServiceDefaultsEnabled bool
	peerServiceMappings        map[string]string
	// debugAbandonedSpans controls if the tracer should log when old, open spans are found
	debugAbandonedSpans bool
	// spanTimeout represents how old a span can be before it should be logged as a possible
	// misconfiguration. Unused if debugAbandonedSpans is false.
	spanTimeout          time.Duration
	partialFlushMinSpans int
	// partialFlushEnabled specifices whether the tracer should enable partial flushing. Value
	// from DD_TRACE_PARTIAL_FLUSH_ENABLED, default false.
	partialFlushEnabled bool
	// statsComputationEnabled enables client-side stats computation (aka trace metrics).
	statsComputationEnabled      bool
	dataStreamsMonitoringEnabled bool
	// dynamicInstrumentationEnabled controls if the target application can be modified by Dynamic Instrumentation or not.
	dynamicInstrumentationEnabled bool
	// globalSampleRate holds the sample rate for the tracer.
	globalSampleRate float64
	// ciVisibilityEnabled controls if the tracer is loaded with CI Visibility mode. default false
	ciVisibilityEnabled   bool
	ciVisibilityAgentless bool
	// logDirectory is directory for tracer logs
	logDirectory string
	// traceRateLimitPerSecond specifies the rate limit per second for traces.
	traceRateLimitPerSecond float64
	// logToStdout, if true, indicates we should log all traces to the standard output
	logToStdout bool
	// isLambdaFunction, if true, indicates we are in a lambda function
	isLambdaFunction bool
	// debugStack enables the collection of debug stack traces globally. Error traces will not record a stack trace when this option is false.
	debugStack bool
	// reportHostname indicates whether hostname should be reported on spans.
	// Set to true when DD_TRACE_REPORT_HOSTNAME=true, or when hostname is explicitly configured via DD_TRACE_SOURCE_HOSTNAME or WithHostname().
	reportHostname bool
	// featureFlags specifies any enabled feature flags.
	featureFlags map[string]struct{}
	// retryInterval is the interval between agent connection retries. It has no effect if sendRetries is not set
	retryInterval time.Duration
	// logsOTelEnabled controls if the OpenTelemetry Logs SDK pipeline should be enabled
	logsOTelEnabled bool
	// traceProtocol is the trace protocol version to use (e.g. TraceProtocolV04 or TraceProtocolV1).
	// Initialized from DD_TRACE_AGENT_PROTOCOL_VERSION, may be downgraded by the tracer
	// if the agent doesn't support the requested version.
	traceProtocol float64
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	cfg := new(Config)
	p := provider.New()

	// Resolve agent URL from DD_TRACE_AGENT_URL, DD_AGENT_HOST, DD_TRACE_AGENT_PORT.
	// All three are read through the provider so telemetry is reported for each.
	agentURLStr := p.GetString("DD_TRACE_AGENT_URL", "")
	agentHost := p.GetString("DD_AGENT_HOST", "")
	agentPort := p.GetString("DD_TRACE_AGENT_PORT", "")
	cfg.agentURL = resolveAgentURL(agentURLStr, agentHost, agentPort)

	cfg.debug = p.GetBool("DD_TRACE_DEBUG", false)
	cfg.logStartup = p.GetBool("DD_TRACE_STARTUP_LOGS", true)
	cfg.serviceName = p.GetString("DD_SERVICE", "")
	cfg.version = p.GetString("DD_VERSION", "")
	cfg.env = p.GetString("DD_ENV", "")
	cfg.serviceMappings = p.GetMap("DD_SERVICE_MAPPING", nil)
	cfg.runtimeMetrics = p.GetBool("DD_RUNTIME_METRICS_ENABLED", false)
	cfg.runtimeMetricsV2 = p.GetBool("DD_RUNTIME_METRICS_V2_ENABLED", true)
	cfg.profilerHotspots = p.GetBool("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", true)
	cfg.profilerEndpoints = p.GetBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", true)
	cfg.spanAttributeSchemaVersion = p.GetInt("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", 0)
	cfg.peerServiceDefaultsEnabled = p.GetBool("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
	cfg.peerServiceMappings = p.GetMap("DD_TRACE_PEER_SERVICE_MAPPING", nil)
	cfg.debugAbandonedSpans = p.GetBool("DD_TRACE_DEBUG_ABANDONED_SPANS", false)
	cfg.spanTimeout = p.GetDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 10*time.Minute)
	cfg.partialFlushMinSpans = p.GetIntWithValidator("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 1000, validatePartialFlushMinSpans)
	cfg.partialFlushEnabled = p.GetBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false)
	cfg.statsComputationEnabled = p.GetBool("DD_TRACE_STATS_COMPUTATION_ENABLED", true)
	cfg.dataStreamsMonitoringEnabled = p.GetBool("DD_DATA_STREAMS_ENABLED", false)
	cfg.dynamicInstrumentationEnabled = p.GetBool("DD_DYNAMIC_INSTRUMENTATION_ENABLED", false)
	cfg.ciVisibilityEnabled = p.GetBool(constants.CIVisibilityEnabledEnvironmentVariable, false)
	cfg.ciVisibilityAgentless = p.GetBool("DD_CIVISIBILITY_AGENTLESS_ENABLED", false)
	cfg.logDirectory = p.GetString("DD_TRACE_LOG_DIRECTORY", "")
	cfg.traceRateLimitPerSecond = p.GetFloatWithValidator("DD_TRACE_RATE_LIMIT", DefaultRateLimit, validateRateLimit)
	cfg.globalSampleRate = p.GetFloatWithValidator("DD_TRACE_SAMPLE_RATE", math.NaN(), validateSampleRate)
	cfg.debugStack = p.GetBool("DD_TRACE_DEBUG_STACK", true)
	cfg.retryInterval = p.GetDuration("DD_TRACE_RETRY_INTERVAL", time.Millisecond)
	cfg.logsOTelEnabled = p.GetBool("DD_LOGS_OTEL_ENABLED", false)
	cfg.traceProtocol = resolveTraceProtocol(p.GetStringWithValidator("DD_TRACE_AGENT_PROTOCOL_VERSION", "0.4", validateTraceProtocolVersion))

	// Parse feature flags from DD_TRACE_FEATURES as a set
	cfg.featureFlags = make(map[string]struct{})
	if featuresStr := p.GetString("DD_TRACE_FEATURES", ""); featuresStr != "" {
		for _, feat := range strings.FieldsFunc(featuresStr, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			cfg.featureFlags[strings.TrimSpace(feat)] = struct{}{}
		}
	}

	// AWS_LAMBDA_FUNCTION_NAME being set indicates that we're running in an AWS Lambda environment.
	// See: https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html
	// TODO: Is it possible that we can just use `v != ""` to configure one setting, `lambdaMode` instead
	if v, ok := env.Lookup("AWS_LAMBDA_FUNCTION_NAME"); ok {
		cfg.logToStdout = true
		if v != "" {
			cfg.isLambdaFunction = true
		}
	}

	// Hostname lookup, if DD_TRACE_REPORT_HOSTNAME is true
	// If the hostname lookup fails, an error is set and the hostname is not reported
	// The tracer will fail to start if the hostname lookup fails when it is explicitly configured
	// to report the hostname.
	if p.GetBool("DD_TRACE_REPORT_HOSTNAME", false) {
		hostname, err := os.Hostname()
		if err != nil {
			log.Warn("unable to look up hostname: %s", err.Error())
			cfg.hostnameLookupError = err
		}
		cfg.hostname = hostname
		cfg.reportHostname = true
	}
	// Check if DD_TRACE_SOURCE_HOSTNAME was explicitly set, it takes precedence over the hostname lookup
	if sourceHostname, ok := env.Lookup("DD_TRACE_SOURCE_HOSTNAME"); ok {
		// Explicitly configured hostname - always report it
		cfg.hostname = sourceHostname
		cfg.reportHostname = true
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

// CreateNew returns a new global configuration instance.
// This function should be used when we need to create a new configuration instance.
// It build a new configuration instance and override the existing one
// loosing any programmatic configuration that would have been applied to the existing instance.
//
// It shouldn't be used to get the global configuration instance to manipulate it but
// should be used when there is a need to reset the global configuration instance.
//
// This is useful when we need to create a new configuration instance when a new product is initialized.
// Each product should have its own configuration instance and apply its own programmatic configuration to it.
//
// If a customer starts multiple tracer with different programmatic configuration only the latest one will be used
// and available globally.
func CreateNew() *Config {
	mu.Lock()
	defer mu.Unlock()
	instance = loadConfig()

	return instance
}

func SetUseFreshConfig(use bool) {
	mu.Lock()
	defer mu.Unlock()
	useFreshConfig = use
}

// RawAgentURL returns a copy of the configured trace agent URL before any
// transport-level rewriting (e.g. unix → http://UDS_...). Use AgentURL()
// for the URL suitable for HTTP requests.
func (c *Config) RawAgentURL() *url.URL {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.agentURL == nil {
		return nil
	}
	u := *c.agentURL
	return &u
}

func (c *Config) SetAgentURL(u *url.URL, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.agentURL = u
	if u != nil {
		configtelemetry.Report("DD_TRACE_AGENT_URL", u.String(), origin)
	}
}

// AgentURL returns the URL to use for HTTP requests to the agent.
// For unix-scheme URLs this rewrites to the http://UDS_... form; otherwise
// it returns a copy of the configured URL.
func (c *Config) AgentURL() *url.URL {
	u := c.RawAgentURL()
	if u != nil && u.Scheme == "unix" {
		return internal.UnixDataSocketURL(u.Path)
	}
	return u
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
	configtelemetry.Report("DD_TRACE_DEBUG", enabled, origin)
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
	configtelemetry.Report("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", enabled, origin)
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
	configtelemetry.Report(traceprof.CodeHotspotsEnvVar, enabled, origin)
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
	configtelemetry.Report("DD_RUNTIME_METRICS_ENABLED", enabled, origin)
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
	configtelemetry.Report("DD_RUNTIME_METRICS_V2_ENABLED", enabled, origin)
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
	configtelemetry.Report("DD_DATA_STREAMS_ENABLED", enabled, origin)
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
	configtelemetry.Report("DD_TRACE_STARTUP_LOGS", enabled, origin)
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
	configtelemetry.Report("DD_TRACE_SAMPLE_RATE", rate, origin)
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
	configtelemetry.Report("DD_TRACE_RATE_LIMIT", rate, origin)
}

// PartialFlushEnabled returns the partial flushing configuration under a single read lock.
func (c *Config) PartialFlushEnabled() (enabled bool, minSpans int) {
	c.mu.RLock()
	enabled = c.partialFlushEnabled
	minSpans = c.partialFlushMinSpans
	c.mu.RUnlock()
	return enabled, minSpans
}

func (c *Config) SetPartialFlushEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partialFlushEnabled = enabled
	configtelemetry.Report("DD_TRACE_PARTIAL_FLUSH_ENABLED", enabled, origin)
}

func (c *Config) SetPartialFlushMinSpans(minSpans int, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partialFlushMinSpans = minSpans
	configtelemetry.Report("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", minSpans, origin)
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
	configtelemetry.Report("DD_TRACE_DEBUG_ABANDONED_SPANS", enabled, origin)
}

func (c *Config) SpanTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.spanTimeout
}

func (c *Config) SetSpanTimeout(timeout time.Duration, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spanTimeout = timeout
	configtelemetry.Report("DD_TRACE_ABANDONED_SPAN_TIMEOUT", timeout, origin)
}

func (c *Config) DebugStack() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debugStack
}

func (c *Config) SetDebugStack(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debugStack = enabled
	configtelemetry.Report("DD_TRACE_DEBUG_STACK", enabled, origin)
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
	configtelemetry.Report("DD_TRACE_STATS_COMPUTATION_ENABLED", enabled, origin)
}

func (c *Config) LogDirectory() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logDirectory
}

func (c *Config) SetLogDirectory(directory string, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logDirectory = directory
	configtelemetry.Report("DD_TRACE_LOG_DIRECTORY", directory, origin)
}

func (c *Config) ReportHostname() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reportHostname
}

func (c *Config) Hostname() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hostname
}

func (c *Config) SetHostname(hostname string, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hostname = hostname
	c.reportHostname = true // Explicitly configured hostname should always be reported
	configtelemetry.Report("DD_TRACE_SOURCE_HOSTNAME", hostname, origin)
}

func (c *Config) HostnameLookupError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hostnameLookupError
}

func (c *Config) Version() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

func (c *Config) SetVersion(version string, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version = version
	configtelemetry.Report("DD_VERSION", version, origin)
}

func (c *Config) Env() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.env
}

func (c *Config) SetEnv(env string, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.env = env
	configtelemetry.Report("DD_ENV", env, origin)
}

func (c *Config) SetFeatureFlags(features []string, origin telemetry.Origin) {
	c.mu.Lock()
	if c.featureFlags == nil {
		c.featureFlags = make(map[string]struct{})
	}
	for _, feat := range features {
		c.featureFlags[strings.TrimSpace(feat)] = struct{}{}
	}
	all := make([]string, 0, len(c.featureFlags))
	for feat := range c.featureFlags {
		all = append(all, feat)
	}
	c.mu.Unlock()

	configtelemetry.Report("DD_TRACE_FEATURES", strings.Join(all, ","), origin)
}

func (c *Config) FeatureFlags() map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return a copy to prevent external modification
	result := make(map[string]struct{}, len(c.featureFlags))
	maps.Copy(result, c.featureFlags)
	return result
}

// HasFeature performs a single feature flag lookup without copying the underlying map.
// This is better than FeatureFlags() for hot paths (e.g., span creation) to avoid per-call allocations.
func (c *Config) HasFeature(feat string) bool {
	c.mu.RLock()
	ff := c.featureFlags
	if ff == nil {
		c.mu.RUnlock()
		return false
	}
	_, ok := ff[strings.TrimSpace(feat)]
	c.mu.RUnlock()
	return ok
}

// ServiceMappings returns a copy of the service mappings map. If no service mappings are set, returns nil.
func (c *Config) ServiceMappings() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return a copy to prevent external modification
	if c.serviceMappings == nil {
		return nil
	}
	result := make(map[string]string, len(c.serviceMappings))
	maps.Copy(result, c.serviceMappings)
	return result
}

// ServiceMapping performs a single mapping lookup without copying the underlying map.
// This is better than ServiceMappings() for hot paths (e.g., span creation) to avoid per-call allocations.
func (c *Config) ServiceMapping(from string) (to string, ok bool) {
	c.mu.RLock()
	m := c.serviceMappings
	if m == nil {
		c.mu.RUnlock()
		return "", false
	}
	to, ok = m[from]
	c.mu.RUnlock()
	return to, ok
}

func (c *Config) SetServiceMapping(from, to string, origin telemetry.Origin) {
	c.mu.Lock()
	if c.serviceMappings == nil {
		c.serviceMappings = make(map[string]string)
	}
	c.serviceMappings[from] = to
	all := make([]string, 0, len(c.serviceMappings))
	for k, v := range c.serviceMappings {
		all = append(all, fmt.Sprintf("%s:%s", k, v))
	}
	c.mu.Unlock()

	configtelemetry.Report("DD_SERVICE_MAPPING", strings.Join(all, ","), origin)
}

func (c *Config) RetryInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retryInterval
}

func (c *Config) SetRetryInterval(interval time.Duration, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryInterval = interval
	configtelemetry.Report("DD_TRACE_RETRY_INTERVAL", interval, origin)
}

func (c *Config) ServiceName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serviceName
}

func (c *Config) SetServiceName(name string, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.serviceName = name
	configtelemetry.Report("DD_SERVICE", name, origin)
}

func (c *Config) CIVisibilityEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityEnabled
}

func (c *Config) SetCIVisibilityEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ciVisibilityEnabled = enabled
	configtelemetry.Report(constants.CIVisibilityEnabledEnvironmentVariable, enabled, origin)
}

func (c *Config) LogsOTelEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logsOTelEnabled
}

func (c *Config) SetLogsOTelEnabled(enabled bool, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logsOTelEnabled = enabled
	configtelemetry.Report("DD_LOGS_OTEL_ENABLED", enabled, origin)
}

func (c *Config) TraceProtocol() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceProtocol
}

func (c *Config) SetTraceProtocol(v float64, origin telemetry.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.traceProtocol = v
	configtelemetry.Report("DD_TRACE_AGENT_PROTOCOL_VERSION", v, origin)
}
