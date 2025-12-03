// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	useFreshConfig atomic.Bool
	instance       atomic.Value
)

// resolveAgentURL resolves the URL for the trace agent in the following priority order:
// 1. DD_TRACE_AGENT_URL if set and valid
// 2. DD_AGENT_HOST:DD_TRACE_AGENT_PORT if either is set
// 3. UDS path if it exists
// 4. Default http://localhost:8126
func resolveAgentURL() *url.URL {
	// Priority 1: DD_TRACE_AGENT_URL
	if agentURL := provider.getString("DD_TRACE_AGENT_URL", ""); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL: %s", err.Error())
		} else {
			switch u.Scheme {
			case "unix", "http", "https":
				return u
			default:
				log.Warn("Unsupported protocol %q in Agent URL %q. Must be one of: http, https, unix.", u.Scheme, agentURL)
			}
		}
	}

	// Priority 2: DD_AGENT_HOST and DD_TRACE_AGENT_PORT
	// Check if either was explicitly provided (from any source: env, declarative config, etc.)
	hostProvided := provider.isConfigured("DD_AGENT_HOST")
	portProvided := provider.isConfigured("DD_TRACE_AGENT_PORT")

	host := provider.getString("DD_AGENT_HOST", internal.DefaultAgentHostname)
	port := provider.getString("DD_TRACE_AGENT_PORT", internal.DefaultTraceAgentPort)

	// Treat empty values as not provided
	if host == "" {
		hostProvided = false
		host = internal.DefaultAgentHostname
	}
	if port == "" {
		portProvided = false
		port = internal.DefaultTraceAgentPort
	}

	httpURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, port),
	}
	if hostProvided || portProvided {
		return httpURL
	}

	// Priority 3: UDS path if it exists
	if _, err := os.Stat(internal.DefaultTraceAgentUDSPath); err == nil {
		return &url.URL{
			Scheme: "unix",
			Path:   internal.DefaultTraceAgentUDSPath,
		}
	}

	// Priority 4: Default
	return httpURL
}

// Config represents global configuration properties.
type Config struct {
	mu sync.RWMutex
	// Config fields are protected by the mutex.
	agentURL                      *url.URL
	debug                         bool
	logStartup                    bool
	serviceName                   string
	version                       string
	env                           string
	serviceMappings               map[string]string
	hostname                      string
	runtimeMetrics                bool
	runtimeMetricsV2              bool
	profilerHotspots              bool
	profilerEndpoints             bool
	spanAttributeSchemaVersion    int
	peerServiceDefaultsEnabled    bool
	peerServiceMappings           map[string]string
	debugAbandonedSpans           bool
	spanTimeout                   time.Duration
	partialFlushMinSpans          int
	partialFlushEnabled           bool
	statsComputationEnabled       bool
	dataStreamsMonitoringEnabled  bool
	dynamicInstrumentationEnabled bool
	globalSampleRate              float64
	ciVisibilityEnabled           bool
	ciVisibilityAgentless         bool
	logDirectory                  string
	traceRateLimitPerSecond       float64
}

// loadConfig initializes and returns a new config by reading from all configured sources.
// This function is NOT thread-safe and should only be called once through Get's sync.Once.
func loadConfig() *Config {
	// TODO: Use defaults from config json instead of hardcoding them here
	cfg := new(Config)

	cfg.agentURL = resolveAgentURL()

	cfg.debug = provider.getBool("DD_TRACE_DEBUG", false)
	cfg.logStartup = provider.getBool("DD_TRACE_STARTUP_LOGS", false)
	cfg.serviceName = provider.getString("DD_SERVICE", "")
	cfg.version = provider.getString("DD_VERSION", "")
	cfg.env = provider.getString("DD_ENV", "")
	cfg.serviceMappings = provider.getMap("DD_SERVICE_MAPPING", nil)
	cfg.hostname = provider.getString("DD_TRACE_SOURCE_HOSTNAME", "")
	cfg.runtimeMetrics = provider.getBool("DD_RUNTIME_METRICS_ENABLED", false)
	cfg.runtimeMetricsV2 = provider.getBool("DD_RUNTIME_METRICS_V2_ENABLED", false)
	cfg.profilerHotspots = provider.getBool("DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", false)
	cfg.profilerEndpoints = provider.getBool("DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", false)
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
	cfg.traceRateLimitPerSecond = provider.getFloat("DD_TRACE_RATE_LIMIT", 0.0)

	return cfg
}

// Get returns the global configuration singleton.
// This function is thread-safe and can be called from multiple goroutines concurrently.
// The configuration is lazily nitialized on first access using sync.Once, ensuring
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

// shared logic for setting - unlock and check c not nil
// func (c *Config) set()

// TODO: Change these not to be methods on the Config struct but rather load the instance
func (c *Config) Debug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.debug
}

func (c *Config) SetDebug(enabled bool) {
	if c != nil { // TODO: Is there a race condition here, checking value of c?
		c.mu.Lock()
		defer c.mu.Unlock()
		c.debug = enabled
		telemetry.RegisterAppConfig("DD_TRACE_DEBUG", enabled, telemetry.OriginCode)
	}
}

func (c *Config) AgentURL() *url.URL {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.agentURL
}

func (c *Config) SetAgentURL(url *url.URL) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// if c not nil
	c.agentURL = url
}
