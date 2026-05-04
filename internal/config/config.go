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
	"reflect"
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
	OriginDefault    = telemetry.OriginDefault
)

// Product identifies which product is setting a config value via programmatic API.
type Product string

const (
	ProductTracer   Product = "tracer"
	ProductProfiler Product = "profiler"
)

// programmaticOverride records which product claimed a field via programmatic API
// and, for the first code-origin write per (field, product), the closure that reverts
// the field to its pre-code state. restore is captured exactly once and preserved
// across same-product rewrites so undoProduct always restores to the true baseline.
type programmaticOverride struct {
	product Product
	value   any
	restore func()
}

// Config represents global configuration properties.
// Config instances should be obtained via Get() which always returns a non-nil value.
// Methods on Config assume a non-nil receiver and will panic if called on nil.
type Config struct {
	mu sync.RWMutex

	// overrides tracks which product claimed each field via programmatic API (OriginCode).
	// Used by snapshotAndCheck to enforce the cross-product gate and to record undo baselines.
	overrides map[string]programmaticOverride

	// Config fields are protected by the mutex.
	agentURL *url.URL
	debug    bool
	// logStartup, when true, causes various startup info to be written when the tracer starts.
	logStartup bool
	// serviceName specifies the name of this application.
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
	globalSampleRate *DynamicConfig[float64]
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
	// traceProtocol is the Datadog trace protocol version (TraceProtocolV04 or TraceProtocolV1).
	// Only meaningful when otlpExportMode is false.
	traceProtocol float64
	// otlpExportMode indicates traces should be exported via OTLP rather than
	// a Datadog protocol.
	otlpExportMode bool
	// otlpTraceURL is the OTLP collector endpoint for traces
	otlpTraceURL string
	// otlpHeaders holds the resolved OTLP trace headers from
	// OTEL_EXPORTER_OTLP_TRACES_HEADERS plus Content-Type: application/x-protobuf.
	otlpHeaders map[string]string
	// traceID128BitEnabled controls if trace IDs are generated as 128-bits or 64-bits.
	traceID128BitEnabled bool
}

// snapshotAndCheck enforces the cross-product gate for programmatic API calls and,
// on the first code-origin write to a field by a given product, records the baseline
// restore closure so the override can later be reverted by undoProduct (invoked
// as part of PrepareForStart).
//
// Returns true if the caller should abort the update (a different product already
// claimed this field). No-op when product is not supplied or origin is not OriginCode.
// The product parameter is variadic for ergonomic pass-through from Set* methods;
// only the first value is used and callers should pass at most one.
//
// restore is the closure the caller passes on every invocation; it is stored only
// on the first write per (field, product). Subsequent writes from the same product
// update the current value but preserve the original restore closure, so the baseline
// always reflects the true pre-any-code state.
//
// Must be called while c.mu is held.
func (c *Config) snapshotAndCheck(field string, origin telemetry.Origin, value any, restore func(), product ...Product) bool {
	if origin != telemetry.OriginCode || len(product) == 0 {
		return false
	}
	p := product[0]
	if prev, exists := c.overrides[field]; exists {
		if prev.product != p {
			if reflect.DeepEqual(prev.value, value) {
				return false
			}
			telemetry.Count(telemetry.NamespaceGeneral, "config.product_conflict", []string{
				"name:" + field,
				"first_product:" + string(prev.product),
				"second_product:" + string(p),
				"first_value:" + fmt.Sprint(prev.value),
				"second_value:" + fmt.Sprint(value),
			}).Submit(1)
			log.Warn("config: %s already set %s via programmatic API; ignoring %s's attempt to override it",
				prev.product, field, p)
			return true
		}
		// Same-product rewrite: update the current value but preserve the
		// original restore closure so undo still reverts to the pre-any-code baseline.
		prev.value = value
		c.overrides[field] = prev
		return false
	}
	c.overrides[field] = programmaticOverride{product: p, value: value, restore: restore}
	return false
}

// checkProductConflict is a thin wrapper around snapshotAndCheck for setters
// that have not yet been migrated to capture undo baselines. It enforces the
// same first-product-wins gate, but fields updated through this path will NOT
// participate in PrepareForStart's undo step — their entries remain in the
// overrides map to preserve ownership, but no restore closure is recorded.
//
// Must be called while c.mu is held.
func (c *Config) checkProductConflict(field string, origin telemetry.Origin, value any, product ...Product) bool {
	return c.snapshotAndCheck(field, origin, value, nil, product...)
}

// undoProduct reverts every programmatic (OriginCode) override recorded for the
// given product back to the value it held before the first code-origin write.
// Only overrides registered with a non-nil restore closure are undone; setters
// that still use the legacy checkProductConflict path retain their values, but
// their ownership entry is released so a subsequent code write can claim the
// field. Overrides owned by other products and values written via non-code
// origins (env vars, defaults, remote config) are left untouched.
//
// Not intended to be called directly by products — PrepareForStart runs this
// as part of the Start ceremony. Unexported to keep the product-facing API
// narrow; same-package tests access it directly.
func (c *Config) undoProduct(p Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for field, ov := range c.overrides {
		if ov.product != p {
			continue
		}
		if ov.restore != nil {
			ov.restore()
		}
		delete(c.overrides, field)
	}
}

// loadEnvInto reads env-backed config values from the provider into c. Fields
// currently claimed by an OriginCode override (tracked in c.overrides) are left
// alone — they represent programmatic writes that outrank env.
//
// Called by loadConfig on initial load and by refreshFromEnv (via
// PrepareForStart) on subsequent product Starts. Callers must hold c.mu.
//
// Telemetry reporting: fields whose setters participate in the new undo path
// (currently DD_ENV) also emit configtelemetry.Report so that post-undo the
// reported value matches the restored in-memory value. Other fields keep their
// current behavior — no configtelemetry.Report on load, only on setter writes.
// That asymmetry is intentional and scoped to the POC; migrating more setters
// to the undo path will add the corresponding reports here.
//
// Env-source coverage: the provider's own internal telemetry reports which
// source (OTEL env, DD env, stable/declarative config, default) produced each
// lookup; that is the authoritative per-source record. Our configtelemetry.Report
// calls record the effective (winning) value+origin after layering.
//
// Edge case: if an env var's value changes between two product Starts while a
// code override is active for that field, the override guard above skips the
// re-read and we do not emit fresh env telemetry. The effective value does not
// change (the code override still wins), so no behavioral regression — only a
// stale env-source report in telemetry until the override is released. Not
// considered a realistic operator workflow, but documented here as a known
// limitation.
func (c *Config) loadEnvInto() {
	p := provider.New()

	// Resolve agent URL from DD_TRACE_AGENT_URL, DD_AGENT_HOST, DD_TRACE_AGENT_PORT.
	// All three are read through the provider so telemetry is reported for each.
	// Guarded as one unit under DD_TRACE_AGENT_URL since that's the field's canonical key.
	if _, ov := c.overrides["DD_TRACE_AGENT_URL"]; !ov {
		agentURLStr := p.GetString("DD_TRACE_AGENT_URL", "")
		agentHost := p.GetString("DD_AGENT_HOST", "")
		agentPort := p.GetString("DD_TRACE_AGENT_PORT", "")
		c.agentURL = resolveAgentURL(agentURLStr, agentHost, agentPort)
	}

	if _, ov := c.overrides["DD_TRACE_DEBUG"]; !ov {
		c.debug = p.GetBool("DD_TRACE_DEBUG", false)
	}
	if _, ov := c.overrides["DD_TRACE_STARTUP_LOGS"]; !ov {
		c.logStartup = p.GetBool("DD_TRACE_STARTUP_LOGS", true)
	}
	if _, ov := c.overrides["DD_SERVICE"]; !ov {
		c.serviceName = p.GetString("DD_SERVICE", "")
	}
	if _, ov := c.overrides["DD_VERSION"]; !ov {
		c.version = p.GetString("DD_VERSION", "")
	}
	// DD_ENV participates in the new undo path: report telemetry here so that
	// post-undo, the backend sees the restored env/default value with its
	// correct origin, not the stale OriginCode value from the last setter write.
	if _, ov := c.overrides["DD_ENV"]; !ov {
		c.env = p.GetString("DD_ENV", "")
		configtelemetry.Report("DD_ENV", c.env, envOrigin(p, "DD_ENV"))
	}
	if _, ov := c.overrides["DD_SERVICE_MAPPING"]; !ov {
		c.serviceMappings = p.GetMap("DD_SERVICE_MAPPING", nil, internal.DDTagsDelimiter)
	}
	if _, ov := c.overrides["DD_RUNTIME_METRICS_ENABLED"]; !ov {
		c.runtimeMetrics = p.GetBool("DD_RUNTIME_METRICS_ENABLED", false)
	}
	if _, ov := c.overrides["DD_RUNTIME_METRICS_V2_ENABLED"]; !ov {
		c.runtimeMetricsV2 = p.GetBool("DD_RUNTIME_METRICS_V2_ENABLED", true)
	}
	if _, ov := c.overrides[traceprof.CodeHotspotsEnvVar]; !ov {
		c.profilerHotspots = p.GetBool("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", true)
	}
	if _, ov := c.overrides["DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"]; !ov {
		c.profilerEndpoints = p.GetBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", true)
	}
	if _, ov := c.overrides["DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED"]; !ov {
		c.peerServiceDefaultsEnabled = p.GetBool("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
	}
	if _, ov := c.overrides["DD_TRACE_PEER_SERVICE_MAPPING"]; !ov {
		c.peerServiceMappings = p.GetMap("DD_TRACE_PEER_SERVICE_MAPPING", nil, internal.DDTagsDelimiter)
	}
	if _, ov := c.overrides["DD_TRACE_DEBUG_ABANDONED_SPANS"]; !ov {
		c.debugAbandonedSpans = p.GetBool("DD_TRACE_DEBUG_ABANDONED_SPANS", false)
	}
	if _, ov := c.overrides["DD_TRACE_ABANDONED_SPAN_TIMEOUT"]; !ov {
		c.spanTimeout = p.GetDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 10*time.Minute)
	}
	if _, ov := c.overrides["DD_TRACE_PARTIAL_FLUSH_MIN_SPANS"]; !ov {
		c.partialFlushMinSpans = p.GetIntWithValidator("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 1000, validatePartialFlushMinSpans)
	}
	if _, ov := c.overrides["DD_TRACE_PARTIAL_FLUSH_ENABLED"]; !ov {
		c.partialFlushEnabled = p.GetBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false)
	}
	if _, ov := c.overrides["DD_TRACE_STATS_COMPUTATION_ENABLED"]; !ov {
		c.statsComputationEnabled = p.GetBool("DD_TRACE_STATS_COMPUTATION_ENABLED", true)
	}
	if _, ov := c.overrides["DD_DATA_STREAMS_ENABLED"]; !ov {
		c.dataStreamsMonitoringEnabled = p.GetBool("DD_DATA_STREAMS_ENABLED", false)
	}
	if _, ov := c.overrides["DD_DYNAMIC_INSTRUMENTATION_ENABLED"]; !ov {
		c.dynamicInstrumentationEnabled = p.GetBool("DD_DYNAMIC_INSTRUMENTATION_ENABLED", false)
	}
	if _, ov := c.overrides[constants.CIVisibilityEnabledEnvironmentVariable]; !ov {
		c.ciVisibilityEnabled = p.GetBool(constants.CIVisibilityEnabledEnvironmentVariable, false)
	}
	if _, ov := c.overrides["DD_CIVISIBILITY_AGENTLESS_ENABLED"]; !ov {
		c.ciVisibilityAgentless = p.GetBool("DD_CIVISIBILITY_AGENTLESS_ENABLED", false)
	}
	if _, ov := c.overrides["DD_TRACE_LOG_DIRECTORY"]; !ov {
		c.logDirectory = p.GetString("DD_TRACE_LOG_DIRECTORY", "")
	}
	if _, ov := c.overrides["DD_TRACE_RATE_LIMIT"]; !ov {
		c.traceRateLimitPerSecond = p.GetFloatWithValidator("DD_TRACE_RATE_LIMIT", DefaultRateLimit, validateRateLimit)
	}
	if _, ov := c.overrides["DD_TRACE_DEBUG_STACK"]; !ov {
		c.debugStack = p.GetBool("DD_TRACE_DEBUG_STACK", true)
	}
	if _, ov := c.overrides["DD_TRACE_RETRY_INTERVAL"]; !ov {
		c.retryInterval = p.GetDuration("DD_TRACE_RETRY_INTERVAL", time.Millisecond)
	}
	if _, ov := c.overrides["DD_LOGS_OTEL_ENABLED"]; !ov {
		c.logsOTelEnabled = p.GetBool("DD_LOGS_OTEL_ENABLED", false)
	}
	if _, ov := c.overrides["DD_TRACE_AGENT_PROTOCOL_VERSION"]; !ov {
		c.traceProtocol = resolveTraceProtocol(p.GetStringWithValidator("DD_TRACE_AGENT_PROTOCOL_VERSION", TraceProtocolVersionStringV04, validateTraceProtocolVersion))
	}
	if _, ov := c.overrides["OTEL_TRACES_EXPORTER"]; !ov {
		c.otlpExportMode = p.GetString("OTEL_TRACES_EXPORTER", "") == "otlp"
		// DD_TRACE_AGENT_PROTOCOL_VERSION overrides OTEL_TRACES_EXPORTER
		if p.IsSet("DD_TRACE_AGENT_PROTOCOL_VERSION") {
			c.otlpExportMode = false
		}
	}
	// otlpTraceURL and otlpHeaders have no setter / no override key — always refresh.
	c.otlpTraceURL = resolveOTLPTraceURL(c.agentURL, p.GetString("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", ""))
	c.otlpHeaders = buildOTLPHeaders(p.GetMap("OTEL_EXPORTER_OTLP_TRACES_HEADERS", nil, internal.OtelTagsDelimeter))
	c.traceID128BitEnabled = p.GetBool("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", true)
}

// envOrigin returns OriginEnvVar if key is set in the environment, else OriginDefault.
func envOrigin(p *provider.Provider, key string) telemetry.Origin {
	if p.IsSet(key) {
		return telemetry.OriginEnvVar
	}
	return telemetry.OriginDefault
}

// refreshFromEnv re-reads env-backed config values into the singleton,
// skipping fields currently claimed by code-origin overrides (tracked in
// c.overrides). Called by PrepareForStart after undoProduct so env changes
// between Start invocations are picked up on restart.
//
// Not intended to be called directly by products — PrepareForStart runs this
// as part of the Start ceremony. Unexported to keep the product-facing API
// narrow; same-package tests access it directly.
//
// TODO(perf): PrepareForStart currently invokes this on every product Start,
// which re-reads env vars even on cold startup where Get's lazy loadConfig
// already did the work. A per-product `initialized atomic.Bool` in each
// product's package would let Start gate the undo+refresh behind
// `started.Swap(true)`, so cold first-Starts skip both and only re-inits pay
// the refresh cost. Not done here to keep the POC scope small.
func (c *Config) refreshFromEnv() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadEnvInto()
}

// PrepareForStart resets and refreshes the Config so that the given product
// can apply its Start options on top. It:
//
//  1. Rolls back any programmatic (OriginCode) overrides this product recorded
//     on a previous Start, leaving other products' overrides intact.
//  2. Re-reads env-backed values so env changes between Starts are picked up.
//
// Products should call this at the top of their Start, after obtaining the
// singleton via Get. Callers that only need to read the current configuration
// (e.g. contribs) should use Get alone and not invoke this method.
//
//	c := internalconfig.Get()
//	c.PrepareForStart(internalconfig.ProductTracer)
//	// ... apply product Start options on top of c
//
// The underlying undo and refresh steps are internal details; they are not
// exported so products cannot accidentally invoke only one half of the
// ceremony or misorder them.
func (c *Config) PrepareForStart(product Product) {
	c.undoProduct(product)
	c.refreshFromEnv()
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	cfg := &Config{
		overrides: make(map[string]programmaticOverride),
	}
	cfg.loadEnvInto()
	p := provider.New()

	sampleRate, sampleRateOrigin := p.GetFloatWithValidatorOrigin("DD_TRACE_SAMPLE_RATE", math.NaN(), validateSampleRate)
	cfg.globalSampleRate = newDynamicConfig("trace_sample_rate", sampleRate, sampleRateOrigin, equalFloat)

	// Parse feature flags from DD_TRACE_FEATURES as a set
	cfg.featureFlags = make(map[string]struct{})
	if featuresStr := p.GetString("DD_TRACE_FEATURES", ""); featuresStr != "" {
		for _, feat := range strings.FieldsFunc(featuresStr, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			cfg.featureFlags[strings.TrimSpace(feat)] = struct{}{}
		}
	}

	if schemaStr := p.GetString("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", ""); schemaStr != "" {
		if v, ok := parseSpanAttributeSchema(schemaStr); ok {
			cfg.spanAttributeSchemaVersion = v
		}
	}
	if cfg.spanAttributeSchemaVersion >= 1 {
		cfg.peerServiceDefaultsEnabled = true
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

func (c *Config) SetAgentURL(u *url.URL, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_AGENT_URL", origin, u, product...) {
		return
	}
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

func (c *Config) SetDebug(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_DEBUG", origin, enabled, product...) {
		return
	}
	c.debug = enabled
	configtelemetry.Report("DD_TRACE_DEBUG", enabled, origin)
}

func (c *Config) ProfilerEndpoints() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilerEndpoints
}

func (c *Config) SetProfilerEndpoints(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", origin, enabled, product...) {
		return
	}
	c.profilerEndpoints = enabled
	configtelemetry.Report("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", enabled, origin)
}

func (c *Config) ProfilerHotspotsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profilerHotspots
}

func (c *Config) SetProfilerHotspotsEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict(traceprof.CodeHotspotsEnvVar, origin, enabled, product...) {
		return
	}
	c.profilerHotspots = enabled
	configtelemetry.Report(traceprof.CodeHotspotsEnvVar, enabled, origin)
}
func (c *Config) RuntimeMetricsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetrics
}

func (c *Config) SetRuntimeMetricsEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_RUNTIME_METRICS_ENABLED", origin, enabled, product...) {
		return
	}
	c.runtimeMetrics = enabled
	configtelemetry.Report("DD_RUNTIME_METRICS_ENABLED", enabled, origin)
}

func (c *Config) RuntimeMetricsV2Enabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsV2
}

func (c *Config) SetRuntimeMetricsV2Enabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_RUNTIME_METRICS_V2_ENABLED", origin, enabled, product...) {
		return
	}
	c.runtimeMetricsV2 = enabled
	configtelemetry.Report("DD_RUNTIME_METRICS_V2_ENABLED", enabled, origin)
}

func (c *Config) DataStreamsMonitoringEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dataStreamsMonitoringEnabled
}

func (c *Config) SetDataStreamsMonitoringEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_DATA_STREAMS_ENABLED", origin, enabled, product...) {
		return
	}
	c.dataStreamsMonitoringEnabled = enabled
	configtelemetry.Report("DD_DATA_STREAMS_ENABLED", enabled, origin)
}

func (c *Config) LogStartup() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logStartup
}

func (c *Config) SetLogStartup(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_STARTUP_LOGS", origin, enabled, product...) {
		return
	}
	c.logStartup = enabled
	configtelemetry.Report("DD_TRACE_STARTUP_LOGS", enabled, origin)
}

func (c *Config) LogToStdout() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logToStdout
}

func (c *Config) SetLogToStdout(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("logToStdout", origin, enabled, product...) {
		return
	}
	c.logToStdout = enabled
}

func (c *Config) IsLambdaFunction() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isLambdaFunction
}

func (c *Config) SetIsLambdaFunction(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("isLambdaFunction", origin, enabled, product...) {
		return
	}
	c.isLambdaFunction = enabled
}

func (c *Config) GlobalSampleRate() float64 {
	return c.globalSampleRate.Get()
}

// GlobalSampleRateConfig returns the DynamicConfig for the global sample rate.
// Products use this to apply RC updates and read telemetry snapshots.
func (c *Config) GlobalSampleRateConfig() *DynamicConfig[float64] {
	return c.globalSampleRate
}

func (c *Config) SetGlobalSampleRate(rate float64, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_SAMPLE_RATE", origin, rate, product...) {
		return
	}
	c.globalSampleRate.setBaseline(rate, origin)
	configtelemetry.Report("DD_TRACE_SAMPLE_RATE", rate, origin)
}

func (c *Config) TraceRateLimitPerSecond() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceRateLimitPerSecond
}

func (c *Config) SetTraceRateLimitPerSecond(rate float64, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_RATE_LIMIT", origin, rate, product...) {
		return
	}
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

func (c *Config) SetPartialFlushEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_PARTIAL_FLUSH_ENABLED", origin, enabled, product...) {
		return
	}
	c.partialFlushEnabled = enabled
	configtelemetry.Report("DD_TRACE_PARTIAL_FLUSH_ENABLED", enabled, origin)
}

func (c *Config) SetPartialFlushMinSpans(minSpans int, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", origin, minSpans, product...) {
		return
	}
	c.partialFlushMinSpans = minSpans
	configtelemetry.Report("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", minSpans, origin)
}

func (c *Config) DebugAbandonedSpans() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debugAbandonedSpans
}

func (c *Config) SetDebugAbandonedSpans(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_DEBUG_ABANDONED_SPANS", origin, enabled, product...) {
		return
	}
	c.debugAbandonedSpans = enabled
	configtelemetry.Report("DD_TRACE_DEBUG_ABANDONED_SPANS", enabled, origin)
}

func (c *Config) SpanTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.spanTimeout
}

func (c *Config) SetSpanTimeout(timeout time.Duration, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_ABANDONED_SPAN_TIMEOUT", origin, timeout, product...) {
		return
	}
	c.spanTimeout = timeout
	configtelemetry.Report("DD_TRACE_ABANDONED_SPAN_TIMEOUT", timeout, origin)
}

func (c *Config) DebugStack() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debugStack
}

func (c *Config) SetDebugStack(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_DEBUG_STACK", origin, enabled, product...) {
		return
	}
	c.debugStack = enabled
	configtelemetry.Report("DD_TRACE_DEBUG_STACK", enabled, origin)
}

func (c *Config) StatsComputationEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statsComputationEnabled
}

func (c *Config) SetStatsComputationEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_STATS_COMPUTATION_ENABLED", origin, enabled, product...) {
		return
	}
	c.statsComputationEnabled = enabled
	configtelemetry.Report("DD_TRACE_STATS_COMPUTATION_ENABLED", enabled, origin)
}

func (c *Config) LogDirectory() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logDirectory
}

func (c *Config) SetLogDirectory(directory string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_LOG_DIRECTORY", origin, directory, product...) {
		return
	}
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

func (c *Config) SetHostname(hostname string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_SOURCE_HOSTNAME", origin, hostname, product...) {
		return
	}
	c.hostname = hostname
	c.reportHostname = true
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

func (c *Config) SetVersion(version string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_VERSION", origin, version, product...) {
		return
	}
	c.version = version
	configtelemetry.Report("DD_VERSION", version, origin)
}

func (c *Config) Env() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.env
}

func (c *Config) SetEnv(env string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	priorVal := c.env
	// Silent restore — telemetry is re-reported by the next refreshFromEnv
	// (invoked via PrepareForStart on each product Start) or by a subsequent
	// code write.
	restore := func() {
		c.env = priorVal
	}
	if c.snapshotAndCheck("DD_ENV", origin, env, restore, product...) {
		return
	}
	c.env = env
	configtelemetry.Report("DD_ENV", env, origin)
}

// SetFeatureFlags adds to the feature flag set. No cross-product gate because this is additive, not a replacement.
func (c *Config) SetFeatureFlags(features []string, origin telemetry.Origin, product ...Product) {
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

// SetServiceMapping adds a single service mapping entry. No cross-product gate because this is additive, not a replacement.
func (c *Config) SetServiceMapping(from, to string, origin telemetry.Origin, product ...Product) {
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

// SpanAttributeSchemaVersion returns the configured DD_TRACE_SPAN_ATTRIBUTE_SCHEMA version.
// Read on the span-creation hot path; avoids defer to minimise lock cost.
func (c *Config) SpanAttributeSchemaVersion() int {
	c.mu.RLock()
	v := c.spanAttributeSchemaVersion
	c.mu.RUnlock()
	return v
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
	configtelemetry.Report("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", enabled, origin)
}

// PeerServiceMappings returns a copy of the peer service mappings map. If no mappings are set, returns nil.
// Not intended for hot paths — use PeerServiceMapping for single-key lookups to avoid per-call allocations.
func (c *Config) PeerServiceMappings() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.peerServiceMappings == nil {
		return nil
	}
	result := make(map[string]string, len(c.peerServiceMappings))
	maps.Copy(result, c.peerServiceMappings)
	return result
}

// PeerServiceMapping performs a single mapping lookup without copying the underlying map.
// This is better than PeerServiceMappings() for hot paths to avoid per-call allocations.
func (c *Config) PeerServiceMapping(from string) (to string, ok bool) {
	c.mu.RLock()
	m := c.peerServiceMappings
	if m == nil {
		c.mu.RUnlock()
		return "", false
	}
	to, ok = m[from]
	c.mu.RUnlock()
	return to, ok
}

func (c *Config) SetPeerServiceMappings(mappings map[string]string, origin telemetry.Origin) {
	c.mu.Lock()
	c.peerServiceMappings = make(map[string]string, len(mappings))
	maps.Copy(c.peerServiceMappings, mappings)
	all := make([]string, 0, len(c.peerServiceMappings))
	for k, v := range c.peerServiceMappings {
		all = append(all, fmt.Sprintf("%s:%s", k, v))
	}
	c.mu.Unlock()

	configtelemetry.Report("DD_TRACE_PEER_SERVICE_MAPPING", strings.Join(all, ","), origin)
}

func (c *Config) SetPeerServiceMapping(from, to string, origin telemetry.Origin) {
	c.mu.Lock()
	if c.peerServiceMappings == nil {
		c.peerServiceMappings = make(map[string]string)
	}
	c.peerServiceMappings[from] = to
	all := make([]string, 0, len(c.peerServiceMappings))
	for k, v := range c.peerServiceMappings {
		all = append(all, fmt.Sprintf("%s:%s", k, v))
	}
	c.mu.Unlock()

	configtelemetry.Report("DD_TRACE_PEER_SERVICE_MAPPING", strings.Join(all, ","), origin)
}

func (c *Config) RetryInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retryInterval
}

func (c *Config) SetRetryInterval(interval time.Duration, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_RETRY_INTERVAL", origin, interval, product...) {
		return
	}
	c.retryInterval = interval
	configtelemetry.Report("DD_TRACE_RETRY_INTERVAL", interval, origin)
}

func (c *Config) ServiceName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serviceName
}

func (c *Config) SetServiceName(name string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_SERVICE", origin, name, product...) {
		return
	}
	c.serviceName = name
	configtelemetry.Report("DD_SERVICE", name, origin)
}

func (c *Config) CIVisibilityEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ciVisibilityEnabled
}

func (c *Config) SetCIVisibilityEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict(constants.CIVisibilityEnabledEnvironmentVariable, origin, enabled, product...) {
		return
	}
	c.ciVisibilityEnabled = enabled
	configtelemetry.Report(constants.CIVisibilityEnabledEnvironmentVariable, enabled, origin)
}

func (c *Config) LogsOTelEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logsOTelEnabled
}

func (c *Config) SetLogsOTelEnabled(enabled bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_LOGS_OTEL_ENABLED", origin, enabled, product...) {
		return
	}
	c.logsOTelEnabled = enabled
	configtelemetry.Report("DD_LOGS_OTEL_ENABLED", enabled, origin)
}

func (c *Config) TraceProtocol() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceProtocol
}

func (c *Config) SetTraceProtocol(v float64, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_TRACE_AGENT_PROTOCOL_VERSION", origin, v, product...) {
		return
	}
	c.traceProtocol = v
	configtelemetry.Report("DD_TRACE_AGENT_PROTOCOL_VERSION", v, origin)
}

func (c *Config) OTLPTraceURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpTraceURL
}

func (c *Config) OTLPExportMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.otlpExportMode
}

func (c *Config) SetOTLPExportMode(v bool, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("OTEL_TRACES_EXPORTER", origin, v, product...) {
		return
	}
	c.otlpExportMode = v
	configtelemetry.Report("OTEL_TRACES_EXPORTER", v, origin)
}

// OTLPHeaders returns a copy of the OTLP headers map. If no headers are set, returns nil.
// Safe to return the full map because it is not called in hot paths.
func (c *Config) OTLPHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.otlpHeaders)
}

func (c *Config) TraceID128BitEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traceID128BitEnabled
}
