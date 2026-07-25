// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"cmp"
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
)

const (
	openFeatureProviderEnabledEnv  = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED"
	openFeatureSpanEnrichmentEnv   = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED"
	openFeatureEvaluationCountsEnv = "DD_FLAGGING_EVALUATION_COUNTS_ENABLED"
)

var openFeatureProviderBinding = ConsumerBinding{
	ID:       "openfeature.provider",
	Consumer: "the OpenFeature provider constructor",
	Keys: []string{
		"DD_SERVICE",
		"DD_ENV",
		"DD_VERSION",
		openFeatureProviderEnabledEnv,
		openFeatureSpanEnrichmentEnv,
		openFeatureEvaluationCountsEnv,
	},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

func init() {
	registerRaw(RawDefinition{
		Key:       openFeatureSpanEnrichmentEnv,
		Sources:   SourceEnvironment,
		Telemetry: TelemetryReport,
	})
	registerRaw(RawDefinition{
		Key:       openFeatureEvaluationCountsEnv,
		Sources:   SourceEnvironment,
		Telemetry: TelemetryReport,
	})
	registerBinding(openFeatureProviderBinding)
}

// OpenFeatureSnapshot is the immutable configuration sampled for one
// OpenFeature provider construction.
type OpenFeatureSnapshot struct {
	Service                     string
	Environment                 string
	Version                     string
	ProviderEnabled             bool
	SpanEnrichmentEnabled       bool
	FlagEvaluationCountsEnabled bool
}

type openFeatureResolution uint8

const (
	openFeaturePublicResolution openFeatureResolution = iota
	openFeatureProviderResolution
	openFeatureContextResolution
)

// ResolveOpenFeatureSnapshot samples and reports the public, gated provider
// constructor configuration.
func ResolveOpenFeatureSnapshot() OpenFeatureSnapshot {
	snapshot, events := resolveOpenFeatureSnapshot(openFeaturePublicResolution)
	reportInstrumentationEvents(events)
	return snapshot
}

// ResolveOpenFeatureProviderSnapshot samples and reports the ungated provider
// construction configuration used by compatibility helpers.
func ResolveOpenFeatureProviderSnapshot() OpenFeatureSnapshot {
	snapshot, events := resolveOpenFeatureSnapshot(openFeatureProviderResolution)
	reportInstrumentationEvents(events)
	return snapshot
}

// ResolveOpenFeatureContextSnapshot samples and reports only the service
// context used by standalone writer compatibility helpers.
func ResolveOpenFeatureContextSnapshot() OpenFeatureSnapshot {
	snapshot, events := resolveOpenFeatureSnapshot(openFeatureContextResolution)
	reportInstrumentationEvents(events)
	return snapshot
}

func resolveOpenFeatureSnapshot(resolution openFeatureResolution) (OpenFeatureSnapshot, []ConfigEvent) {
	p := newDirectEnvProvider()
	var (
		snapshot OpenFeatureSnapshot
		events   []ConfigEvent
	)

	if resolution == openFeaturePublicResolution {
		enabled, local := resolveBoolWithProvider(
			p,
			registeredDefinition(openFeatureProviderEnabledEnv),
			openFeatureProviderBinding,
			false,
		)
		events = append(events, local...)
		snapshot.ProviderEnabled = enabled.Winner.Value
		if !snapshot.ProviderEnabled {
			return snapshot, events
		}
	}

	service, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_SERVICE"),
		openFeatureProviderBinding,
	)
	events = append(events, local...)
	executable, _ := os.Executable()
	snapshot.Service = cmp.Or(service.Winner.Value, globalconfig.ServiceName(), executable)

	environment, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_ENV"),
		openFeatureProviderBinding,
	)
	events = append(events, local...)
	snapshot.Environment = environment.Winner.Value

	version, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_VERSION"),
		openFeatureProviderBinding,
	)
	events = append(events, local...)
	snapshot.Version = version.Winner.Value

	if resolution == openFeatureContextResolution {
		return snapshot, events
	}

	spanEnrichment, local := resolveBoolWithProvider(
		p,
		registeredDefinition(openFeatureSpanEnrichmentEnv),
		openFeatureProviderBinding,
		false,
	)
	events = append(events, local...)
	snapshot.SpanEnrichmentEnabled = spanEnrichment.Winner.Value

	evaluationCounts, local := resolveBoolWithProvider(
		p,
		registeredDefinition(openFeatureEvaluationCountsEnv),
		openFeatureProviderBinding,
		true,
	)
	events = append(events, local...)
	snapshot.FlagEvaluationCountsEnabled = evaluationCounts.Winner.Value

	return snapshot, events
}
