// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	internal "github.com/DataDog/appsec-internal-go/appsec"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func init() {
	registerSCAAppConfigTelemetry()
}

// Register the global app telemetry configuration related to the Software Composition Analysis (SCA) product.
// Report over telemetry whether SCA's enablement env var was set or not along with its value. Nothing is reported in
// case of an error or if the env var is not set.
func registerSCAAppConfigTelemetry() {
	enabled, origin := stableconfig.BoolStableConfig(EnvSCAEnabled, false)
	telemetry.RegisterAppConfig(EnvSCAEnabled, enabled, origin)
}

// The following environment variables dictate the enablement of different the ASM products.
const (
	// EnvEnabled controls ASM Threats Protection's enablement.
	EnvEnabled = "DD_APPSEC_ENABLED"
	// EnvSCAEnabled controls ASM Software Composition Analysis (SCA)'s enablement.
	EnvSCAEnabled = "DD_APPSEC_SCA_ENABLED"
)

// StartOption is used to customize the AppSec configuration when invoked with appsec.Start()
type StartOption func(c *StartConfig)

type StartConfig struct {
	// RC is the remote config client configuration to be used.
	RC *remoteconfig.ClientConfig
	// IsEnabled is a function that determines whether AppSec is enabled or not. When unset, the
	// default [IsEnabled] function is used.
	EnablementMode func() (EnablementMode, telemetry.Origin, error)
	// MetaStructAvailable is true if meta struct is supported by the trace agent.
	MetaStructAvailable bool

	APISecOptions []internal.APISecOption

	// BlockingUnavailable is true when the application run in an environment where blocking is not possible
	BlockingUnavailable bool
}

type EnablementMode int8

const (
	// ForcedOff is the mode where AppSec is forced to be disabled, not allowing remote activation.
	ForcedOff EnablementMode = -1
	// RCStandby is the mode where AppSec is in stand-by, waiting remote activation.
	RCStandby EnablementMode = 0
	// ForcedOn is the mode where AppSec is forced to be enabled.
	ForcedOn EnablementMode = 1
)

func NewStartConfig(opts ...StartOption) *StartConfig {
	c := &StartConfig{
		EnablementMode: func() (mode EnablementMode, origin telemetry.Origin, err error) {
			enabled, set, err := IsEnabledByEnvironment()
			if set {
				origin = telemetry.OriginEnvVar
				if enabled {
					mode = ForcedOn
				} else {
					mode = ForcedOff
				}
			} else {
				origin = telemetry.OriginDefault
				mode = RCStandby
			}
			return mode, origin, err
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithEnablementMode forces AppSec enablement, replacing the default initialization conditions
// implemented by [IsEnabledByEnvironment].
func WithEnablementMode(mode EnablementMode) StartOption {
	return func(c *StartConfig) {
		c.EnablementMode = func() (EnablementMode, telemetry.Origin, error) {
			return mode, telemetry.OriginCode, nil
		}
	}
}

// WithRCConfig sets the AppSec remote config client configuration to the specified cfg
func WithRCConfig(cfg remoteconfig.ClientConfig) StartOption {
	return func(c *StartConfig) {
		c.RC = &cfg
	}
}

func WithMetaStructAvailable(available bool) StartOption {
	return func(c *StartConfig) {
		c.MetaStructAvailable = available
	}
}

func WithAPISecOptions(opts ...internal.APISecOption) StartOption {
	return func(c *StartConfig) {
		c.APISecOptions = append(c.APISecOptions, opts...)
	}
}

func WithBlockingUnavailable(unavailable bool) StartOption {
	return func(c *StartConfig) {
		c.BlockingUnavailable = unavailable
	}
}

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
	// RC is the remote configuration client used to receive product configuration updates. Nil if RC is disabled (default)
	RC   *remoteconfig.ClientConfig
	RASP bool
	// SupportedAddresses are the addresses that the AppSec listener will bind to.
	SupportedAddresses AddressSet
	// MetaStructAvailable is true if meta struct is supported by the trace agent.
	MetaStructAvailable bool
	// BlockingUnavailable is true when the application run in an environment where blocking is not possible
	BlockingUnavailable bool
}

// AddressSet is a set of WAF addresses.
type AddressSet map[string]struct{}

func NewAddressSet(addrs []string) AddressSet {
	set := make(AddressSet, len(addrs))
	for _, addr := range addrs {
		set[addr] = struct{}{}
	}
	return set
}

// AnyOf returns true if any of the addresses in the set are in the given list.
func (set AddressSet) AnyOf(anyOf ...string) bool {
	for _, addr := range anyOf {
		if _, ok := set[addr]; ok {
			return true
		}
	}

	return false
}

// IsEnabledByEnvironment returns true when appsec is enabled by the environment variable
// [EnvEnabled] being set to a truthy value, as well as whether the environment variable was set at
// all or not (so it is possible to distinguish between explicitly false, and false-by-default).
// If the [EnvEnabled] variable is set to a value that is not a valid boolean (according to
// [strconv.ParseBool]), it is considered false-y, and a detailed error is also returned.
func IsEnabledByEnvironment() (enabled bool, set bool, err error) {
	return parseBoolEnvVar(EnvEnabled)
}

// Return true when the given environment variable is defined and set to true (as of strconv's
// parsing rules). When false, it also returns whether the env var was actually set or not.
// In case of a parsing error, it returns a detailed error.
func parseBoolEnvVar(env string) (enabled bool, set bool, err error) {
	str, set := os.LookupEnv(env)
	if str == "" {
		return false, set, nil
	} else if enabled, err = strconv.ParseBool(str); err != nil {
		return false, set, fmt.Errorf("could not parse %s value `%s` as a boolean value", env, str)
	}

	return enabled, set, nil
}

// NewConfig returns a fresh appsec configuration read from the env
func (c *StartConfig) NewConfig() (*Config, error) {
	rules, err := internal.RulesFromEnv()
	if err != nil {
		return nil, err
	}

	r, err := NewRulesManager(rules)
	if err != nil {
		return nil, err
	}

	return &Config{
		RulesManager:        r,
		WAFTimeout:          internal.WAFTimeoutFromEnv(),
		TraceRateLimit:      int64(internal.RateLimitFromEnv()),
		Obfuscator:          internal.NewObfuscatorConfig(),
		APISec:              internal.NewAPISecConfig(c.APISecOptions...),
		RASP:                internal.RASPEnabled(),
		RC:                  c.RC,
		MetaStructAvailable: c.MetaStructAvailable,
		BlockingUnavailable: c.BlockingUnavailable,
	}, nil
}
