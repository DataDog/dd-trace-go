// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"fmt"
	"log/slog"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

func init() {
	registerSCAAppConfigTelemetry()
}

// registerSCAAppConfigTelemetry reports invalid SCA enablement configuration.
// Valid configured values are reported by the singleton's config provider.
func registerSCAAppConfigTelemetry() {
	if err := internalconfig.Get().AppSecSCAEnabledError(); err != nil {
		telemetrylog.Error("appsec: failed to get SCA config", slog.Any("error", telemetrylog.NewSafeError(err)))
	}
}

// The following environment variables dictate the enablement of different the ASM products.
const (
	// EnvEnabled controls ASM Threats Protection's enablement.
	EnvEnabled = "DD_APPSEC_ENABLED"
	// EnvSCAEnabled controls ASM Software Composition Analysis (SCA)'s enablement.
	EnvSCAEnabled = "DD_APPSEC_SCA_ENABLED"
	// EnvAgenticOnboarding is set by Datadog's agentic onboarding solution when it configures App &
	// API Protection. It carries no behavior; its value is reported in configuration telemetry only.
	EnvAgenticOnboarding = "DD_APPSEC_AGENTIC_ONBOARDING"
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

	APISecOptions []APISecOption

	// BlockingUnavailable is true when the application run in an environment where blocking is not possible
	BlockingUnavailable bool

	// ProxyEnvironment is true if the application is running in a proxy environment,
	// such as within an Envoy External Processor.
	ProxyEnvironment bool
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

func WithAPISecOptions(opts ...APISecOption) StartOption {
	return func(c *StartConfig) {
		c.APISecOptions = append(c.APISecOptions, opts...)
	}
}

func WithBlockingUnavailable(unavailable bool) StartOption {
	return func(c *StartConfig) {
		c.BlockingUnavailable = unavailable
	}
}

func WithProxyEnvironment() StartOption {
	return func(c *StartConfig) {
		c.APISecOptions = append(c.APISecOptions, WithProxy())
	}
}

// Config is the AppSec configuration.
type Config struct {
	*WAFManager

	// WAFTimeout is the maximum WAF execution time
	WAFTimeout time.Duration
	// TraceRateLimit is the AppSec trace rate limit (traces per second).
	TraceRateLimit int64
	// APISec configuration
	APISec APISecConfig
	// RC is the remote configuration client used to receive product configuration updates. Nil if RC is disabled (default)
	RC *remoteconfig.ClientConfig
	// RASP determines whether RASP features are enabled or not.
	RASP bool
	// SupportedAddresses are the addresses that the AppSec listener will bind to.
	SupportedAddresses AddressSet
	// MetaStructAvailable is true if meta struct is supported by the trace agent.
	MetaStructAvailable bool
	// BlockingUnavailable is true when the application run in an environment where blocking is not possible
	BlockingUnavailable bool
	// TracingAsTransport is true if APM is disabled and manually force keeping a trace is the only way for it to be sent.
	TracingAsTransport bool
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

// IsEnabledByEnvironment returns true when AppSec is enabled by DD_APPSEC_ENABLED,
// whether the value was configured, and any boolean parsing error.
func IsEnabledByEnvironment() (enabled bool, set bool, err error) {
	enabled, origin, err := internalconfig.Get().AppSecEnabled()
	if origin != telemetry.OriginDefault {
		set = true
	}
	return enabled, set, err
}

// NewConfig returns a fresh AppSec configuration.
func (c *StartConfig) NewConfig() (*Config, error) {
	data, err := RulesFromEnv()
	if err != nil {
		return nil, fmt.Errorf("reading WAF rules from environment: %w", err)
	}
	manager, err := NewWAFManagerWithStaticRules(NewObfuscatorConfig(), data)
	if err != nil {
		return nil, err
	}

	return &Config{
		WAFManager:          manager,
		WAFTimeout:          WAFTimeoutFromEnv(),
		TraceRateLimit:      RateLimitFromEnv(),
		APISec:              NewAPISecConfig(c.APISecOptions...),
		RASP:                RASPEnabled(),
		RC:                  c.RC,
		MetaStructAvailable: c.MetaStructAvailable,
		BlockingUnavailable: c.BlockingUnavailable,
		TracingAsTransport:  !internalconfig.Get().APMTracingEnabled(),
	}, nil
}
