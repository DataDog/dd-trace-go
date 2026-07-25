// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package config

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Configuration environment variables
const (
	// EnvAPISecEnabled is the env var used to enable API Security
	EnvAPISecEnabled = "DD_API_SECURITY_ENABLED"
	// EnvAPISecSampleRate is the env var used to set the sampling rate of API Security schema extraction.
	// Deprecated: a new [APISecConfig.Sampler] is now used instead of this.
	EnvAPISecSampleRate = "DD_API_SECURITY_REQUEST_SAMPLE_RATE"
	// EnvAPISecProxySampleRate is the env var used to set the sampling rate of API Security schema extraction for proxies.
	// The value represents the number of schemas extracted per minute (samples per minute).
	EnvAPISecProxySampleRate = "DD_API_SECURITY_PROXY_SAMPLE_RATE"
	// EnvAPISecDownstreamRequestBodyAnalysisSampleRate Defines the probability of a downstream request body being sampled,
	// or said differently, defines the overall number of requests for which the request and response body should be sampled / analysed (50%).
	EnvAPISecDownstreamRequestBodyAnalysisSampleRate = "DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE"
	// EnvAPISecMaxDownstreamRequestBodyAnalysis The maximum number of downstream requests per request for which the request and response body should be analysed.
	EnvAPISecMaxDownstreamRequestBodyAnalysis = "DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS"
	// EnvObfuscatorKey is the env var used to provide the WAF key obfuscation regexp
	EnvObfuscatorKey = "DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP"
	// EnvObfuscatorValue is the env var used to provide the WAF value obfuscation regexp
	EnvObfuscatorValue = "DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP"
	// EnvWAFTimeout is the env var used to specify the timeout value for a WAF run
	EnvWAFTimeout = "DD_APPSEC_WAF_TIMEOUT"
	// EnvTraceRateLimit is the env var used to set the ASM trace limiting rate
	EnvTraceRateLimit = "DD_APPSEC_TRACE_RATE_LIMIT"
	// EnvRules is the env var used to provide a path to a local security rule file
	EnvRules = "DD_APPSEC_RULES"
	// EnvRASPEnabled is the env var used to enable/disable RASP functionalities for ASM
	EnvRASPEnabled = "DD_APPSEC_RASP_ENABLED"

	// envAPISecSampleDelay is the env var used to set the delay for the API Security sampler in system tests.
	// It is not indended to be set by users.
	envAPISecSampleDelay = "DD_API_SECURITY_SAMPLE_DELAY"
)

// Configuration constants and default values
const (
	// DefaultAPISecSampleRate is the default rate at which API Security schemas are extracted from requests
	DefaultAPISecSampleRate = internalconfig.AppSecDefaultAPISecuritySampleRate
	// DefaultAPISecSampleInterval is the default interval between two samples being taken.
	DefaultAPISecSampleInterval = internalconfig.AppSecDefaultAPISecuritySampleInterval
	// DefaultAPISecProxySampleRate is the default rate (schemas per minute) at which API Security schemas are extracted from requests
	DefaultAPISecProxySampleRate = internalconfig.AppSecDefaultAPISecurityProxySampleRate
	// DefaultAPISecProxySampleInterval is the default time window for the API Security proxy sampler rate limiter.
	DefaultAPISecProxySampleInterval = time.Minute
	// DefaultDownstreamRequestBodyAnalysisSampleRate is the default sample rate for downstream request body analysis per incoming request.
	DefaultDownstreamRequestBodyAnalysisSampleRate = internalconfig.AppSecDefaultDownstreamRequestBodyAnalysisSampleRate
	// DefaultMaxDownstreamRequestBodyAnalysis is the default maximum size in bytes of downstream request body to be analyzed.
	DefaultMaxDownstreamRequestBodyAnalysis = internalconfig.AppSecDefaultMaxDownstreamRequestBodyAnalysis
	// DefaultObfuscatorKeyRegex is the default regexp used to obfuscate keys
	DefaultObfuscatorKeyRegex = internalconfig.AppSecDefaultObfuscatorKeyRegex
	// DefaultObfuscatorValueRegex is the default regexp used to obfuscate values
	DefaultObfuscatorValueRegex = internalconfig.AppSecDefaultObfuscatorValueRegex
	// DefaultWAFTimeout is the default time limit past which a WAF run will timeout
	DefaultWAFTimeout = internalconfig.AppSecDefaultWAFTimeout
	// DefaultTraceRate is the default limit (trace/sec) past which ASM traces are sampled out
	DefaultTraceRate = internalconfig.AppSecDefaultTraceRate // up to 100 appsec traces/s
)

// APISecConfig holds the configuration for API Security schemas reporting.
// It is used to enabled/disable the feature.
type APISecConfig struct {
	Sampler apisec.Sampler
	Enabled bool
	IsProxy bool
	// Deprecated: use the new [APISecConfig.Sampler] instead.
	SampleRate float64
	// DownstreamRequestBodyAnalysisSampleRate is the sample rate for downstream request body analysis per incoming request.
	DownstreamRequestBodyAnalysisSampleRate float64
	// MaxDownstreamRequestBodyAnalysis is the maximum size in bytes of downstream request body to be analyzed.
	MaxDownstreamRequestBodyAnalysis int
}

// ObfuscatorConfig wraps the key and value regexp to be passed to the WAF to perform obfuscation.
type ObfuscatorConfig struct {
	KeyRegex   string
	ValueRegex string
}

type APISecOption func(*APISecConfig)

// NewAPISecConfig creates and returns a new API Security configuration by reading the env
func NewAPISecConfig(opts ...APISecOption) APISecConfig {
	snapshot, events := internalconfig.ResolveAppSecAPISecuritySnapshot()
	internalconfig.ReportAppSecDiagnostics(events)
	return newAPISecConfig(snapshot, opts...)
}

func newAPISecConfig(snapshot internalconfig.AppSecAPISecuritySnapshot, opts ...APISecOption) APISecConfig {
	cfg := APISecConfig{
		Enabled:                                 snapshot.Enabled,
		DownstreamRequestBodyAnalysisSampleRate: snapshot.DownstreamRequestBodyAnalysisSampleRate,
		MaxDownstreamRequestBodyAnalysis:        snapshot.MaxDownstreamRequestBodyAnalysis,
		SampleRate:                              snapshot.SampleRate,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if !snapshot.RASPEnabled {
		log.Debug("appsec: RASP functionalities are disabled, disabling API Security downward request body analysis")
		cfg.DownstreamRequestBodyAnalysisSampleRate = 0.0
		cfg.MaxDownstreamRequestBodyAnalysis = 0
	}

	if cfg.Sampler != nil {
		return cfg
	}

	if cfg.IsProxy {
		rate, _ := internalconfig.ResolveAPISecuritySamplerConfig(true)
		cfg.Sampler = apisec.NewProxySampler(rate, DefaultAPISecProxySampleInterval)
	} else {
		_, interval := internalconfig.ResolveAPISecuritySamplerConfig(false)
		cfg.Sampler = apisec.NewSampler(interval)
	}

	return cfg
}

func readAPISecuritySampleRate() float64 {
	rate, events := internalconfig.ResolveAPISecuritySampleRate()
	internalconfig.ReportAppSecDiagnostics(events)
	return rate
}

// WithAPISecSampler sets the sampler for the API Security configuration. This is useful for testing
// purposes.
func WithAPISecSampler(sampler apisec.Sampler) APISecOption {
	return func(c *APISecConfig) {
		c.Sampler = sampler
	}
}

// WithProxy configures API Security for a proxy environment.
func WithProxy() APISecOption {
	return func(c *APISecConfig) {
		c.IsProxy = true
	}
}

// RASPEnabled returns true if RASP functionalities are enabled through the env, or if DD_APPSEC_RASP_ENABLED
// is not set
func RASPEnabled() bool {
	enabled, _, events := internalconfig.ResolveAppSecRASPEnabled()
	internalconfig.ReportAppSecDiagnostics(events)
	return enabled
}

// NewObfuscatorConfig creates and returns a new WAF obfuscator configuration by reading the env
func NewObfuscatorConfig() ObfuscatorConfig {
	snapshot, events := internalconfig.ResolveAppSecObfuscatorSnapshot()
	internalconfig.ReportAppSecDiagnostics(events)
	return ObfuscatorConfig{KeyRegex: snapshot.KeyRegex, ValueRegex: snapshot.ValueRegex}
}

// WAFTimeoutFromEnv reads and parses the WAF timeout value set through the env
// If not set, it defaults to `DefaultWAFTimeout`
func WAFTimeoutFromEnv() (timeout time.Duration) {
	timeout, _, events := internalconfig.ResolveAppSecWAFTimeout()
	internalconfig.ReportAppSecDiagnostics(events)
	return timeout
}

// RateLimitFromEnv reads and parses the trace rate limit set through the env
// If not set, it defaults to `DefaultTraceRate`
func RateLimitFromEnv() (rate int64) {
	rate, _, events := internalconfig.ResolveAppSecTraceRateLimit()
	internalconfig.ReportAppSecDiagnostics(events)
	return rate
}

// RulesFromEnv returns the security rules provided through the environment
// If the env var is not set, the default recommended rules are returned instead
func RulesFromEnv() ([]byte, error) {
	rules, _, err, _, events := internalconfig.ResolveAppSecRules()
	internalconfig.ReportAppSecDiagnostics(events)
	if err != nil {
		return nil, err
	}
	if rules == nil {
		log.Debug("appsec: using the default built-in recommended security rules")
		return nil, nil
	}
	log.Debug("appsec: using the configured security rules")
	return rules, nil
}
