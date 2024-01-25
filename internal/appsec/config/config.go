// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
	"os"
	"strconv"
	"time"

	internal "github.com/DataDog/appsec-internal-go/appsec"
)

// EnvEnabled is the env var used to enable/disable appsec
const EnvEnabled = "DD_APPSEC_ENABLED"

// Config is the AppSec configuration.
type Config struct {
	// rules loaded via the env var DD_APPSEC_RULES. When not set, the builtin rules will be used
	// and live-updated with remote configuration.
	RulesManager *RulesManager
	// Maximum WAF execution time
	WAFTimeout time.Duration
	// AppSec trace rate limit (traces per second).
	TraceRateLimit int64
	// Obfuscator configuration
	Obfuscator internal.ObfuscatorConfig
	// APISec configuration
	APISec internal.APISecConfig
	// RCEnabled is true when remote configuration is enabled
	RCEnabled bool
	// CodeActivation options.WithCodeActivation(true) or options.WithCodeActivation(false), nil when not set
	CodeActivation *bool
	// EnvVarActivation `DD_APPSEC_ENABLED` state, false, not set, or true
	EnvVarActivation *bool
}

// NewConfig returns a fresh appsec configuration read from the env
func NewConfig() (*Config, error) {
	rules, err := internal.RulesFromEnv()
	if err != nil {
		return nil, err
	}

	r, err := NewRulesManeger(rules)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		RulesManager:     r,
		WAFTimeout:       internal.WAFTimeoutFromEnv(),
		TraceRateLimit:   int64(internal.RateLimitFromEnv()),
		Obfuscator:       internal.NewObfuscatorConfig(),
		APISec:           internal.NewAPISecConfig(),
		RCEnabled:        remoteconfig.IsUpAndRunning(),
		CodeActivation:   nil,
		EnvVarActivation: IsEnabledViaEnvVar(),
	}

	cfg.logInconsistencies()
	return cfg, nil
}

// logInconsistencies logs inconsistencies in the config
func (e *Config) logInconsistencies() {
	if e.EnvVarActivation != nil && e.CodeActivation != nil && *e.EnvVarActivation != *e.CodeActivation {
		log.Warn("appsec: inconsistent configuration: DD_APPSEC_ENABLED env var is set to %v but options.WithCodeActivation in appsec.Start() is set to %v; Appsec will be disabled", *e.EnvVarActivation, *e.CodeActivation)
	}

	if e.APISec.Enabled && e.APISec.SampleRate == 0 {
		log.Warn("appsec: inconsistent configuration: APISec is enabled but the sample rate is set to 0")
	}
}

// IsAppsecExplicitelyEnabled returns true when appsec is enabled intentionally by the user in the starting environment or code.
// using either `DD_APPSEC_ENABLED=true` or `appsec.Start(options.WithCodeActivation(true))`
func (e *Config) IsAppsecExplicitelyEnabled() bool {
	return e.EnvVarActivation != nil && *e.EnvVarActivation || e.CodeActivation != nil && *e.CodeActivation
}

// IsAppsecExplicitelyDisabled returns true when appsec is disabled intentionally by the user in the starting environment or code.
// using either `DD_APPSEC_ENABLED=false` or `appsec.Start(options.WithCodeActivation(false))`
func (e *Config) IsAppsecExplicitelyDisabled() bool {
	return e.EnvVarActivation != nil && !*e.EnvVarActivation || e.CodeActivation != nil && !*e.CodeActivation
}

// CanAppsecBeEnabledLater returns true when appsec can be enabled later in the process.
// This is the case when the env var is not set and the code activation is not set and remote configuration is enabled.
func (e *Config) CanAppsecBeEnabledLater() bool {
	return e.EnvVarActivation == nil && e.CodeActivation == nil && e.RCEnabled
}

// IsEnabledViaEnvVar returns true when appsec is enabled when the environment variable
// DD_APPSEC_ENABLED is set to true.
// It also returns whether the env var is actually set in the env or not.
func IsEnabledViaEnvVar() *bool {
	enabledStr, set := os.LookupEnv(EnvEnabled)
	if enabledStr == "" || !set {
		return nil
	}

	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		fmt.Errorf("could not parse %s value `%s` as a boolean value", EnvEnabled, enabledStr)
		return nil
	}

	return &enabled
}
