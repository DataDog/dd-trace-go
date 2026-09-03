// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
)

// Source identifies which delivery mechanism feeds feature-flag configuration
// to the provider.
type Source int

const (
	// SourceDisabled means no configuration source should be started.
	SourceDisabled Source = iota
	// SourceAgentless means configuration is polled directly from Datadog over HTTPS.
	SourceAgentless
	// SourceRemoteConfig means configuration is delivered through the Agent's Remote Config.
	SourceRemoteConfig
)

// Settings is the resolved, ready-to-use configuration for starting a
// feature-flag delivery source.
type Settings struct {
	Source Source

	// AgentlessBaseURL is the configured Agentless base URL override.
	// SENSITIVE: may embed credentials; never log.
	AgentlessBaseURL string
	PollInterval     time.Duration
	RequestTimeout   time.Duration

	Site   string
	APIKey string
	Env    string

	// LegacyKeyDecided reports whether DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED
	// was the deciding input for Source.
	LegacyKeyDecided bool
}

// ResolveSettings resolves the feature-flag delivery Settings from cfg.
func ResolveSettings(cfg *config.Config) Settings {
	resolved, legacyDecided := resolveSourceFromConfig(cfg)

	return Settings{
		Source:           resolved,
		AgentlessBaseURL: cfg.FeatureFlagsAgentlessBaseURL(),
		PollInterval:     cfg.FeatureFlagsAgentlessPollInterval(),
		RequestTimeout:   cfg.FeatureFlagsAgentlessRequestTimeout(),
		Site:             cfg.Site(),
		APIKey:           cfg.APIKey(),
		Env:              cfg.Env(),
		LegacyKeyDecided: legacyDecided,
	}
}

// resolveSourceFromConfig reads only the four config fields that feed resolveSource,
// so callers that just need the delivery source don't pay for the unrelated reads
// ResolveSettings performs. legacyDecided has the same meaning as in resolveSource.
func resolveSourceFromConfig(cfg *config.Config) (resolved Source, legacyDecided bool) {
	enabled, enabledSet := cfg.FeatureFlagsEnabled()
	source, sourceSet := cfg.FeatureFlagsConfigurationSource()
	legacyEnabled := cfg.ExperimentalFlaggingProviderEnabled()
	legacyEnabledSet := cfg.ExperimentalFlaggingProviderEnabledExplicit()

	return resolveSource(sourceInputs{
		enabled:          enabled,
		enabledSet:       enabledSet,
		source:           source,
		sourceSet:        sourceSet,
		legacyEnabled:    legacyEnabled,
		legacyEnabledSet: legacyEnabledSet,
	})
}

// RemoteConfigSourceSelected reports whether cfg resolves to SourceRemoteConfig.
// It lets the tracer decide whether to keep its eager Remote Config subscription
// without importing this package's full Settings machinery.
func RemoteConfigSourceSelected(cfg *config.Config) bool {
	source, _ := resolveSourceFromConfig(cfg)
	return source == SourceRemoteConfig
}

// sourceInputs is the pure, table-testable input to resolveSource.
type sourceInputs struct {
	enabled    bool
	enabledSet bool

	source    string // raw, untrimmed
	sourceSet bool

	legacyEnabled    bool
	legacyEnabledSet bool
}

// resolveSource applies the fail-closed precedence rules for selecting a
// delivery Source. legacyDecided reports whether the deprecated
// DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED key was the deciding input.
func resolveSource(in sourceInputs) (source Source, legacyDecided bool) {
	// 1. The stable kill switch wins over everything.
	if in.enabledSet && !in.enabled {
		return SourceDisabled, false
	}

	// 2 & 3. An explicit, non-blank source value is authoritative. Any value
	// that isn't one of the recognized ones fails closed rather than falling
	// back to the default, since silently defaulting a typo would start
	// billed polling.
	if in.sourceSet {
		if s := strings.ToLower(strings.TrimSpace(in.source)); s != "" {
			switch s {
			case "agentless":
				return SourceAgentless, false
			case "remote_config":
				return SourceRemoteConfig, false
			case "offline":
				return SourceDisabled, false
			default:
				return SourceDisabled, false
			}
		}
	}

	// 4. Explicitly enabling must not imply the historical remote_config source.
	if in.enabledSet {
		return SourceAgentless, false
	}

	// 5. Grandfather legacy adopters who opted in when RCM was the only source.
	if in.legacyEnabledSet {
		if in.legacyEnabled {
			return SourceRemoteConfig, true
		}
		return SourceDisabled, true
	}

	// 6. Nothing set.
	return SourceAgentless, false
}
