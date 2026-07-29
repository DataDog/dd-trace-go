// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
)

// These aliases keep registry declarations and package tests concise. Provider
// and other dependency packages import schema directly.
type (
	RawDefinition    = schema.RawDefinition
	ConsumerBinding  = schema.ConsumerBinding
	SourcePolicy     = schema.SourcePolicy
	TelemetryPolicy  = schema.TelemetryPolicy
	SamplingBoundary = schema.SamplingBoundary
	ConfigEvent      = schema.ConfigEvent
	EventKind        = schema.EventKind
	ReportCadence    = schema.ReportCadence
)

const (
	SourceEnvironment = schema.SourceEnvironment
	SourceStable      = schema.SourceStable

	TelemetryReport      = schema.TelemetryReport
	TelemetryRedact      = schema.TelemetryRedact
	TelemetrySanitizeURL = schema.TelemetrySanitizeURL
	TelemetryOmit        = schema.TelemetryOmit

	SamplePackageInit        = schema.SamplePackageInit
	SampleTracerConstruction = schema.SampleTracerConstruction
	SampleProductStart       = schema.SampleProductStart
	SampleConstructor        = schema.SampleConstructor
	SampleFirstUse           = schema.SampleFirstUse
	SamplePerCall            = schema.SamplePerCall

	EventConfiguration  = schema.EventConfiguration
	EventOTelEnvHiding  = schema.EventOTelEnvHiding
	EventOTelEnvInvalid = schema.EventOTelEnvInvalid

	ReportNever             = schema.ReportNever
	ReportOncePerGeneration = schema.ReportOncePerGeneration
	ReportOnChange          = schema.ReportOnChange
)

type registry struct {
	raw      []RawDefinition
	bindings []ConsumerBinding

	freezeOnce sync.Once
	freezeErr  error
	frozen     atomic.Bool
}

func newRegistry() *registry {
	return new(registry)
}

func (r *registry) addRaw(def RawDefinition) {
	if r.frozen.Load() {
		panic("config registry is frozen")
	}
	r.raw = append(r.raw, def)
}

func (r *registry) addBinding(binding ConsumerBinding) {
	if r.frozen.Load() {
		panic("config registry is frozen")
	}
	binding.Keys = append([]string(nil), binding.Keys...)
	r.bindings = append(r.bindings, binding)
}

func (r *registry) validateAndFreeze() error {
	r.freezeOnce.Do(func() {
		r.frozen.Store(true)
		r.freezeErr = r.validate()
	})
	return r.freezeErr
}

func (r *registry) mustValidateAndFreeze() {
	if err := r.validateAndFreeze(); err != nil {
		panic("config registry: " + err.Error())
	}
}

func (r *registry) validate() error {
	raw := make(map[string]struct{}, len(r.raw))
	for _, def := range r.raw {
		if def.Key == "" {
			return errors.New("raw definition has an empty key")
		}
		if _, exists := raw[def.Key]; exists {
			return fmt.Errorf("duplicate raw key %q", def.Key)
		}
		if def.Sources > SourceStable {
			return fmt.Errorf("raw key %q has invalid source policy %d", def.Key, def.Sources)
		}
		if def.Telemetry > TelemetryOmit {
			return fmt.Errorf("raw key %q has invalid telemetry policy %d", def.Key, def.Telemetry)
		}
		raw[def.Key] = struct{}{}
	}

	bindingIDs := make(map[string]struct{}, len(r.bindings))
	bound := make(map[string]struct{}, len(r.raw))
	for _, binding := range r.bindings {
		if binding.ID == "" {
			return errors.New("consumer binding has an empty ID")
		}
		if _, exists := bindingIDs[binding.ID]; exists {
			return fmt.Errorf("duplicate binding ID %q", binding.ID)
		}
		if binding.Consumer == "" {
			return fmt.Errorf("binding %q has an empty consumer", binding.ID)
		}
		if len(binding.Keys) == 0 {
			return fmt.Errorf("binding %q has no raw keys", binding.ID)
		}
		if binding.Sampling > SamplePerCall {
			return fmt.Errorf("binding %q has invalid sampling boundary %d", binding.ID, binding.Sampling)
		}
		for _, key := range binding.Keys {
			if _, exists := raw[key]; !exists {
				return fmt.Errorf("binding %q references unregistered raw key %q", binding.ID, key)
			}
			bound[key] = struct{}{}
		}
		bindingIDs[binding.ID] = struct{}{}
	}
	for _, def := range r.raw {
		if _, exists := bound[def.Key]; !exists {
			return fmt.Errorf("raw key %q has no consumer binding", def.Key)
		}
	}
	return nil
}

func (r *registry) definitions() ([]RawDefinition, []ConsumerBinding) {
	r.mustValidateAndFreeze()
	raw := append([]RawDefinition(nil), r.raw...)
	bindings := make([]ConsumerBinding, len(r.bindings))
	for i, binding := range r.bindings {
		bindings[i] = binding
		bindings[i].Keys = append([]string(nil), binding.Keys...)
	}
	sort.Slice(raw, func(i, j int) bool {
		return raw[i].Key < raw[j].Key
	})
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].ID < bindings[j].ID
	})
	return raw, bindings
}

func (r *registry) rawDefinition(key string) (RawDefinition, bool) {
	r.mustValidateAndFreeze()
	return r.rawDefinitionFrozen(key)
}

func (r *registry) rawDefinitionFrozen(key string) (RawDefinition, bool) {
	for _, definition := range r.raw {
		if definition.Key == key {
			return definition, true
		}
	}
	return RawDefinition{}, false
}

func (r *registry) reporterMetadata(bindingID, key string) (ConsumerBinding, RawDefinition, bool) {
	r.mustValidateAndFreeze()
	var found ConsumerBinding
	for _, binding := range r.bindings {
		if binding.ID == bindingID {
			found = binding
			break
		}
	}
	if found.ID == "" {
		return ConsumerBinding{}, RawDefinition{}, false
	}
	matches := false
	for _, bindingKey := range found.Keys {
		if bindingKey == key {
			matches = true
			break
		}
	}
	if !matches {
		return ConsumerBinding{}, RawDefinition{}, false
	}
	definition, ok := r.rawDefinitionFrozen(key)
	return found, definition, ok
}

func (r *registry) reporterBinding(binding ConsumerBinding) (reporterBinding, bool) {
	r.mustValidateAndFreeze()
	definitions := make(map[string]reporterDefinition, len(binding.Keys))
	for _, key := range binding.Keys {
		definition, ok := r.rawDefinitionFrozen(key)
		if !ok {
			return reporterBinding{}, false
		}
		definitions[definition.Key] = reporterDefinition{
			name:   definition.Key,
			policy: definition.Telemetry,
		}
	}
	return reporterBinding{
		id:          binding.ID,
		definitions: definitions,
		cadence:     reportCadence(binding),
	}, true
}

var definitionsRegistry = newRegistry()

var tracerSourceHostnameBinding = ConsumerBinding{
	ID:       "tracer.DD_TRACE_SOURCE_HOSTNAME",
	Consumer: "tracer",
	Keys:     []string{"DD_TRACE_SOURCE_HOSTNAME"},
	Sampling: SampleTracerConstruction,
}

func registerRaw(def RawDefinition) {
	definitionsRegistry.addRaw(def)
}

func registerBinding(binding ConsumerBinding) {
	definitionsRegistry.addBinding(binding)
}

// RegisteredDefinitions returns sorted defensive copies of the runtime
// configuration registry.
func RegisteredDefinitions() ([]RawDefinition, []ConsumerBinding) {
	return definitionsRegistry.definitions()
}

func init() {
	registerRaw(RawDefinition{Key: "DD_AGENT_HOST", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_KEY", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APP_KEY", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APM_TRACING_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_AGENTLESS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_AGENTLESS_URL", Sources: SourceStable, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_USE_NOOP_TRACER", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_DATA_STREAMS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_DOGSTATSD_HOST", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_DOGSTATSD_PORT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_DOGSTATSD_URL", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_DYNAMIC_INSTRUMENTATION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_ENV", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_SHA", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_GIT_REPOSITORY_URL", Sources: SourceEnvironment, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "DD_LOGS_OTEL_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_METRICS_OTEL_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_RUNTIME_METRICS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_RUNTIME_METRICS_V2_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_SERVICE_MAPPING", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_SITE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TAGS", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ABANDONED_SPAN_TIMEOUT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AGENT_PORT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AGENT_PROTOCOL_VERSION", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AGENT_TIMEOUT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AGENT_URL", Sources: SourceStable, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "DD_TRACE_ANALYTICS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_DEBUG", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_DEBUG_ABANDONED_SPANS", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_DEBUG_STACK", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_FEATURES", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_INTERNAL_METRICS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_LOG_DIRECTORY", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_OTEL_SEMANTICS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PARTIAL_FLUSH_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PEER_SERVICE_MAPPING", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PROPAGATION_EXTRACT_FIRST", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PROPAGATION_STYLE_EXTRACT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_PROPAGATION_STYLE_INJECT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_RATE_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_REPORT_HOSTNAME", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_RETRY_INTERVAL", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SAMPLE_RATE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SEND_RETRIES", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SOURCE_HOSTNAME", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STARTUP_LOGS", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_ADDITIONAL_TAGS", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_COMPUTATION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_HTTP_ENDPOINT_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_ORIGIN_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_PEER_TAGS_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_STATS_RESOURCE_CARDINALITY_LIMIT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_UNIVERSAL_VERSION_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_VERSION", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_ENDPOINT", Sources: SourceStable, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_HEADERS", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", Sources: SourceStable, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_HEADERS", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_TRACES_HEADERS", Sources: SourceStable, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "OTEL_LOGS_EXPORTER", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_METRICS_EXPORTER", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_TRACES_EXPORTER", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_TRACES_SPAN_METRICS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})

	registerBinding(ConsumerBinding{ID: "tracer.DD_AGENT_HOST", Consumer: "tracer", Keys: []string{"DD_AGENT_HOST"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_API_KEY", Consumer: "tracer", Keys: []string{"DD_API_KEY"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_APP_KEY", Consumer: "tracer", Keys: []string{"DD_APP_KEY"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_CIVISIBILITY_AGENTLESS_ENABLED", Consumer: "tracer", Keys: []string{"DD_CIVISIBILITY_AGENTLESS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_CIVISIBILITY_AGENTLESS_URL", Consumer: "tracer", Keys: []string{"DD_CIVISIBILITY_AGENTLESS_URL"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_CIVISIBILITY_ENABLED", Consumer: "tracer", Keys: []string{"DD_CIVISIBILITY_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_CIVISIBILITY_USE_NOOP_TRACER", Consumer: "tracer", Keys: []string{"DD_CIVISIBILITY_USE_NOOP_TRACER"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_DATA_STREAMS_ENABLED", Consumer: "tracer", Keys: []string{"DD_DATA_STREAMS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_DOGSTATSD_HOST", Consumer: "tracer", Keys: []string{"DD_DOGSTATSD_HOST"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_DOGSTATSD_PORT", Consumer: "tracer", Keys: []string{"DD_DOGSTATSD_PORT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_DOGSTATSD_URL", Consumer: "tracer", Keys: []string{"DD_DOGSTATSD_URL"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_DYNAMIC_INSTRUMENTATION_ENABLED", Consumer: "tracer", Keys: []string{"DD_DYNAMIC_INSTRUMENTATION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_ENV", Consumer: "tracer", Keys: []string{"DD_ENV"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", Consumer: "tracer", Keys: []string{"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_LOGS_OTEL_ENABLED", Consumer: "tracer", Keys: []string{"DD_LOGS_OTEL_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_METRICS_OTEL_ENABLED", Consumer: "tracer", Keys: []string{"DD_METRICS_OTEL_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED", Consumer: "tracer", Keys: []string{"DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_PROFILING_ENDPOINT_COLLECTION_ENABLED", Consumer: "tracer", Keys: []string{"DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_RUNTIME_METRICS_ENABLED", Consumer: "tracer", Keys: []string{"DD_RUNTIME_METRICS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_RUNTIME_METRICS_V2_ENABLED", Consumer: "tracer", Keys: []string{"DD_RUNTIME_METRICS_V2_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_SERVICE", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_SERVICE_MAPPING", Consumer: "tracer", Keys: []string{"DD_SERVICE_MAPPING"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_SITE", Consumer: "tracer", Keys: []string{"DD_SITE"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TAGS", Consumer: "tracer", Keys: []string{"DD_TAGS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_ABANDONED_SPAN_TIMEOUT", Consumer: "tracer", Keys: []string{"DD_TRACE_ABANDONED_SPAN_TIMEOUT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_AGENT_PORT", Consumer: "tracer", Keys: []string{"DD_TRACE_AGENT_PORT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_AGENT_PROTOCOL_VERSION", Consumer: "tracer", Keys: []string{"DD_TRACE_AGENT_PROTOCOL_VERSION"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_AGENT_TIMEOUT", Consumer: "tracer", Keys: []string{"DD_TRACE_AGENT_TIMEOUT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_AGENT_URL", Consumer: "tracer", Keys: []string{"DD_TRACE_AGENT_URL"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_ANALYTICS_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_ANALYTICS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_DEBUG", Consumer: "tracer", Keys: []string{"DD_TRACE_DEBUG"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_DEBUG_ABANDONED_SPANS", Consumer: "tracer", Keys: []string{"DD_TRACE_DEBUG_ABANDONED_SPANS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_DEBUG_STACK", Consumer: "tracer", Keys: []string{"DD_TRACE_DEBUG_STACK"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_FEATURES", Consumer: "tracer", Keys: []string{"DD_TRACE_FEATURES"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_INTERNAL_METRICS_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_INTERNAL_METRICS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_LOG_DIRECTORY", Consumer: "tracer", Keys: []string{"DD_TRACE_LOG_DIRECTORY"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_OTEL_SEMANTICS_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_OTEL_SEMANTICS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PARTIAL_FLUSH_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_PARTIAL_FLUSH_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Consumer: "tracer", Keys: []string{"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PEER_SERVICE_MAPPING", Consumer: "tracer", Keys: []string{"DD_TRACE_PEER_SERVICE_MAPPING"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", Consumer: "tracer", Keys: []string{"DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PROPAGATION_EXTRACT_FIRST", Consumer: "tracer", Keys: []string{"DD_TRACE_PROPAGATION_EXTRACT_FIRST"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PROPAGATION_STYLE_EXTRACT", Consumer: "tracer", Keys: []string{"DD_TRACE_PROPAGATION_STYLE_EXTRACT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_PROPAGATION_STYLE_INJECT", Consumer: "tracer", Keys: []string{"DD_TRACE_PROPAGATION_STYLE_INJECT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_RATE_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_RATE_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_REPORT_HOSTNAME", Consumer: "tracer", Keys: []string{"DD_TRACE_REPORT_HOSTNAME"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_RETRY_INTERVAL", Consumer: "tracer", Keys: []string{"DD_TRACE_RETRY_INTERVAL"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_SAMPLE_RATE", Consumer: "tracer", Keys: []string{"DD_TRACE_SAMPLE_RATE"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_SEND_RETRIES", Consumer: "tracer", Keys: []string{"DD_TRACE_SEND_RETRIES"}, Sampling: SampleTracerConstruction})
	registerBinding(tracerSourceHostnameBinding)
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", Consumer: "tracer", Keys: []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STARTUP_LOGS", Consumer: "tracer", Keys: []string{"DD_TRACE_STARTUP_LOGS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_ADDITIONAL_TAGS", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_ADDITIONAL_TAGS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_COMPUTATION_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_COMPUTATION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_HTTP_ENDPOINT_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_HTTP_ENDPOINT_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_ORIGIN_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_ORIGIN_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_PEER_TAGS_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_PEER_TAGS_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_STATS_RESOURCE_CARDINALITY_LIMIT", Consumer: "tracer", Keys: []string{"DD_TRACE_STATS_RESOURCE_CARDINALITY_LIMIT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_UNIVERSAL_VERSION_ENABLED", Consumer: "tracer", Keys: []string{"DD_TRACE_UNIVERSAL_VERSION_ENABLED"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", Consumer: "tracer", Keys: []string{"DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.DD_VERSION", Consumer: "tracer", Keys: []string{"DD_VERSION"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_ENDPOINT", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_ENDPOINT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_HEADERS", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_HEADERS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_METRICS_HEADERS", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_METRICS_HEADERS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_EXPORTER_OTLP_TRACES_HEADERS", Consumer: "tracer", Keys: []string{"OTEL_EXPORTER_OTLP_TRACES_HEADERS"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_LOGS_EXPORTER", Consumer: "tracer", Keys: []string{"OTEL_LOGS_EXPORTER"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_METRICS_EXPORTER", Consumer: "tracer", Keys: []string{"OTEL_METRICS_EXPORTER"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_TRACES_EXPORTER", Consumer: "tracer", Keys: []string{"OTEL_TRACES_EXPORTER"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.OTEL_TRACES_SPAN_METRICS_ENABLED", Consumer: "tracer", Keys: []string{"OTEL_TRACES_SPAN_METRICS_ENABLED"}, Sampling: SampleTracerConstruction})

	if err := definitionsRegistry.validate(); err != nil {
		panic("config registry: " + err.Error())
	}
}
