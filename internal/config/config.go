// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal"
	configtelemetry "github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
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

// Config represents global configuration properties.
// Config instances should be obtained via Get() which always returns a non-nil value.
// Methods on Config assume a non-nil receiver and will panic if called on nil.
type Config struct {
	mu sync.RWMutex
	configGenerated // embedded generated fields

	// Fields below are NOT generated — they require custom logic, depend on
	// other fields, or are not driven by environment variables.

	// hostnameLookupError is the error returned by os.Hostname() if it fails
	hostnameLookupError error
	// logToStdout, if true, indicates we should log all traces to the standard output
	logToStdout bool
	// isLambdaFunction, if true, indicates we are in a lambda function
	isLambdaFunction bool
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	cfg := new(Config)
	p := provider.New()

	// Initialize all standard fields from the provider (generated code).
	cfg.loadConfigGenerated(p)

	// === Custom initialization below (fields marked custom_init in supported_configurations.json) ===

	// Resolve agent URL from DD_TRACE_AGENT_URL, DD_AGENT_HOST, DD_TRACE_AGENT_PORT.
	// All three are read through the provider so telemetry is reported for each.
	agentURLStr := p.GetString("DD_TRACE_AGENT_URL", "")
	agentHost := p.GetString("DD_AGENT_HOST", "")
	agentPort := p.GetString("DD_TRACE_AGENT_PORT", "")
	cfg.agentURL = resolveAgentURL(agentURLStr, agentHost, agentPort)

	cfg.spanAttributeSchemaVersion = p.GetInt("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", 0)

	cfg.traceProtocol = resolveTraceProtocol(p.GetStringWithValidator("DD_TRACE_AGENT_PROTOCOL_VERSION", TraceProtocolVersionStringV04, validateTraceProtocolVersion))
	cfg.otlpExportMode = p.GetString("OTEL_TRACES_EXPORTER", "") == "otlp"
	// DD_TRACE_AGENT_PROTOCOL_VERSION overrides OTEL_TRACES_EXPORTER
	if p.IsSet("DD_TRACE_AGENT_PROTOCOL_VERSION") {
		cfg.otlpExportMode = false
	}
	cfg.otlpTraceURL = resolveOTLPTraceURL(cfg.agentURL, p.GetString("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", ""))
	cfg.otlpHeaders = buildOTLPHeaders(p.GetMap("OTEL_EXPORTER_OTLP_TRACES_HEADERS", nil, internal.OtelTagsDelimeter))

	// Parse feature flags from DD_TRACE_FEATURES as a set
	cfg.featureFlags = make(map[string]struct{})
	if featuresStr := p.GetString("DD_TRACE_FEATURES", ""); featuresStr != "" {
		for _, feat := range strings.FieldsFunc(featuresStr, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			cfg.featureFlags[strings.TrimSpace(feat)] = struct{}{}
		}
	}

	// === Non-env-var fields (not in supported_configurations.json) ===

	// AWS_LAMBDA_FUNCTION_NAME being set indicates that we're running in an AWS Lambda environment.
	// See: https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html
	if v, ok := env.Lookup("AWS_LAMBDA_FUNCTION_NAME"); ok {
		cfg.logToStdout = true
		if v != "" {
			cfg.isLambdaFunction = true
		}
	}

	// Hostname lookup, if DD_TRACE_REPORT_HOSTNAME is true
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

// === Custom accessors below (not generated — require special logic) ===

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

// PartialFlushEnabled returns the partial flushing configuration under a single read lock.
func (c *Config) PartialFlushEnabled() (enabled bool, minSpans int) {
	c.mu.RLock()
	enabled = c.partialFlushEnabled
	minSpans = c.partialFlushMinSpans
	c.mu.RUnlock()
	return enabled, minSpans
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

// PeerServiceMappings returns a copy of the peer service mappings map.
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

// PeerServiceMapping performs a single peer service mapping lookup.
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

// OTLPHeaders returns a copy of the OTLP headers map. If no headers are set, returns nil.
// Safe to return the full map because it is not called in hot paths.
func (c *Config) OTLPHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.otlpHeaders)
}
