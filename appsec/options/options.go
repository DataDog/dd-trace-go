// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package options

import (
	internalConfig "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"time"
)

type StartOption func(c *internalConfig.Config)

// WithWafTimeout sets the maximum WAF execution time. Can also be set using `DD_APPSEC_WAF_TIMEOUT`.
func WithWafTimeout(timeout time.Duration) StartOption {
	return func(c *internalConfig.Config) {
		c.WAFTimeout = timeout
	}
}

// WithTraceRateLimit sets the AppSec trace rate limit (traces per second). Can also be set using `DD_APPSEC_TRACE_RATE_LIMIT`.
func WithTraceRateLimit(limit int64) StartOption {
	return func(c *internalConfig.Config) {
		c.TraceRateLimit = limit
	}
}

// WithObfuscatorKeyRegex sets the obfuscator regex for all keys in. Can also be set using `DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP`.
func WithObfuscatorKeyRegex(cfg string) StartOption {
	return func(c *internalConfig.Config) {
		c.Obfuscator.KeyRegex = cfg
	}
}

// WithObfuscatorValueRegex sets the obfuscator regex for all values in. Can also be set using `DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP`.
func WithObfuscatorValueRegex(cfg string) StartOption {
	return func(c *internalConfig.Config) {
		c.Obfuscator.ValueRegex = cfg
	}
}

// WithAPISecEnabled enables the APISec feature. Can also be set using `DD_EXPERIMENTAL_API_SECURITY_ENABLED`.
func WithAPISecEnabled() StartOption {
	return func(c *internalConfig.Config) {
		c.APISec.Enabled = true
	}
}

// WithAPISecSampleRate sets the APISec sample rate. Can also be set using `DD_API_SECURITY_REQUEST_SAMPLE_RATE`.
func WithAPISecSampleRate(rate float64) StartOption {
	return func(c *internalConfig.Config) {
		c.APISec.SampleRate = rate
	}
}

// WithRCActive enable or disable Remote Configuration for Appsec Datadog feature.
func WithRCActive(enable bool) StartOption {
	return func(c *internalConfig.Config) {
		c.RCEnabled = enable
	}
}

// WithCodeActivation enable or disable Appsec. Can also be set using `DD_APPSEC_ENABLED`.
func WithCodeActivation(enable bool) StartOption {
	return func(c *internalConfig.Config) {
		c.CodeActivation = &enable
	}
}
