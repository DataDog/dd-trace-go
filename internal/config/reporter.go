// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/urlsanitizer"
)

const redactedTelemetryValue = "[REDACTED]"

type reportKey struct {
	bindingID     string
	name          string
	kind          EventKind
	sourceOrdinal uint16
}

type changeKey struct {
	bindingID     string
	name          string
	kind          EventKind
	sourceOrdinal uint16
}

type reportAction struct {
	event    ConfigEvent
	value    any
	prepared configtelemetry.Prepared
}

type preparedReport struct {
	action     reportAction
	hash       [sha256.Size]byte
	actionable bool
}

type reporterBinding struct {
	policies map[string]TelemetryPolicy
	cadence  ReportCadence
}

// Reporter submits resolved configuration events without retaining user
// values. Its deduplication state is bounded by registered bindings and their
// fixed source attempts.
type Reporter struct {
	mu sync.Mutex

	complete   bool
	bindings   map[string]reporterBinding
	generation map[string]uint64
	once       map[reportKey]struct{}
	changes    map[changeKey][sha256.Size]byte
}

func newReporter() *Reporter {
	return NewReporter()
}

// NewReporter creates a bounded configuration event reporter.
func NewReporter() *Reporter {
	raw, bindings := RegisteredDefinitions()
	return newReporterWithDefinitions(raw, bindings)
}

func newReporterWithDefinitions(raw []RawDefinition, bindings []ConsumerBinding) *Reporter {
	r := new(Reporter)
	r.mu.Lock()
	r.initializeWithDefinitionsLocked(raw, bindings)
	r.mu.Unlock()
	return r
}

func (r *Reporter) initializeWithDefinitionsLocked(raw []RawDefinition, bindings []ConsumerBinding) {
	policies := make(map[string]TelemetryPolicy, len(raw))
	for _, definition := range raw {
		policies[definition.Key] = definition.Telemetry
	}
	registered := make(map[string]reporterBinding, len(bindings))
	for _, binding := range bindings {
		keys := make(map[string]TelemetryPolicy, len(binding.Keys))
		for _, key := range binding.Keys {
			policy, ok := policies[key]
			if !ok {
				continue
			}
			keys[key] = policy
		}
		cadence := ReportOncePerGeneration
		if binding.Sampling == SamplePerCall {
			cadence = ReportOnChange
		}
		registered[binding.ID] = reporterBinding{policies: keys, cadence: cadence}
	}
	r.complete = true
	r.bindings = registered
	r.generation = make(map[string]uint64, len(bindings))
	r.once = make(map[reportKey]struct{})
	r.changes = make(map[changeKey][sha256.Size]byte)
}

// Report transforms, deduplicates, and submits configuration events.
func (r *Reporter) Report(events []ConfigEvent, generation uint64) {
	if !bootstrap.TelemetryEnabled() {
		return
	}

	prepared := make([]preparedReport, 0, len(events))
	for _, event := range events {
		if !validReporterEventShape(event) {
			continue
		}
		if event.Kind == EventConfiguration && event.Policy == TelemetryOmit {
			continue
		}
		var report preparedReport
		if event.Kind == EventConfiguration && !event.ReportValue {
			if event.Cadence != ReportOnChange {
				continue
			}
			reportedEvent := event
			reportedEvent.Value = nil
			reportedEvent.Err = nil
			report = preparedReport{
				action: reportAction{event: reportedEvent},
				hash:   telemetryStateHash(event, nil),
			}
		} else {
			value, ok := transformTelemetryValue(event)
			if !ok {
				continue
			}
			reportedEvent := event
			reportedEvent.Value = nil
			reportedEvent.Err = nil
			report = preparedReport{
				action:     reportAction{event: reportedEvent, value: value},
				actionable: true,
			}
			if event.Cadence == ReportOnChange {
				report.hash = telemetryStateHash(event, value)
			}
		}
		if !r.acceptBinding(event, report.actionable) {
			continue
		}
		prepared = append(prepared, report)
	}

	actions := make([]reportAction, 0, len(prepared))
	r.mu.Lock()
	if len(prepared) != 0 {
		r.initializeStateLocked()
	}
	for _, report := range prepared {
		event := report.action.event
		current, seen := r.generation[event.BindingID]
		if seen && generation < current {
			continue
		}
		if !seen || generation > current {
			r.generation[event.BindingID] = generation
			r.evictBindingOnceState(event.BindingID)
		}
		if !r.shouldReport(event, report.hash) {
			continue
		}
		if report.actionable {
			action := report.action
			if event.Kind == EventConfiguration {
				if event.Origin == telemetry.OriginDefault {
					action.prepared = configtelemetry.PrepareDefault(event.Name)
				} else {
					action.prepared = configtelemetry.PrepareWithID(event.Name, event.Origin, event.ConfigID)
				}
			}
			actions = append(actions, action)
		}
	}
	r.mu.Unlock()

	for _, action := range actions {
		submitReportAction(action)
	}
}

func validReporterEventShape(event ConfigEvent) bool {
	if event.Cadence != ReportOncePerGeneration && event.Cadence != ReportOnChange {
		return false
	}
	if event.Kind > EventOTelEnvInvalid || event.SourceOrdinal > schema.SourceOrdinalMax {
		return false
	}
	if !validReporterOrigin(event) {
		return false
	}
	if event.Kind != EventConfiguration && !provider.IsKnownOTelMapping(event.Name, event.OTelName) {
		return false
	}
	return true
}

func validReporterBinding(event ConfigEvent, binding reporterBinding) bool {
	policy, ok := binding.policies[event.Name]
	return ok && event.Policy == policy && event.Cadence == binding.cadence
}

func (r *Reporter) acceptBinding(event ConfigEvent, cache bool) bool {
	r.mu.Lock()
	if binding, ok := r.bindings[event.BindingID]; ok {
		r.mu.Unlock()
		return validReporterBinding(event, binding)
	}
	if r.complete {
		r.mu.Unlock()
		return false
	}

	binding, definition, ok := definitionsRegistry.reporterMetadata(event.BindingID, event.Name)
	if !ok {
		r.mu.Unlock()
		return false
	}
	cadence := reportCadence(binding)
	if event.Policy != definition.Telemetry || event.Cadence != cadence || !cache {
		r.mu.Unlock()
		return false
	}
	cached, ok := definitionsRegistry.reporterBinding(binding)
	if !ok {
		r.mu.Unlock()
		return false
	}
	if r.bindings == nil {
		r.bindings = make(map[string]reporterBinding)
	}
	r.bindings[binding.ID] = cached
	r.mu.Unlock()
	return true
}

func (r *Reporter) initializeStateLocked() {
	if r.generation == nil {
		r.generation = make(map[string]uint64)
	}
	if r.once == nil {
		r.once = make(map[reportKey]struct{})
	}
	if r.changes == nil {
		r.changes = make(map[changeKey][sha256.Size]byte)
	}
}

func reportCadence(binding ConsumerBinding) ReportCadence {
	if binding.Sampling == SamplePerCall {
		return ReportOnChange
	}
	return ReportOncePerGeneration
}

func validReporterOrigin(event ConfigEvent) bool {
	if event.Kind != EventConfiguration && event.Origin == "" {
		return true
	}
	switch event.Origin {
	case telemetry.OriginDefault,
		telemetry.OriginCode,
		telemetry.OriginDDConfig,
		telemetry.OriginEnvVar,
		telemetry.OriginRemoteConfig,
		telemetry.OriginLocalStableConfig,
		telemetry.OriginManagedStableConfig,
		telemetry.OriginCalculated:
		return true
	default:
		return false
	}
}

func (r *Reporter) shouldReport(event ConfigEvent, hash [sha256.Size]byte) bool {
	switch event.Cadence {
	case ReportOncePerGeneration:
		key := reportKey{
			bindingID: event.BindingID, name: event.Name, kind: event.Kind,
			sourceOrdinal: event.SourceOrdinal,
		}
		if _, exists := r.once[key]; exists {
			return false
		}
		r.once[key] = struct{}{}
		return true
	case ReportOnChange:
		key := changeKey{
			bindingID: event.BindingID, name: event.Name, kind: event.Kind,
			sourceOrdinal: event.SourceOrdinal,
		}
		if previous, exists := r.changes[key]; exists && previous == hash {
			return false
		}
		r.changes[key] = hash
		return true
	default:
		return false
	}
}

func (r *Reporter) evictBindingOnceState(bindingID string) {
	for key := range r.once {
		if key.bindingID == bindingID {
			delete(r.once, key)
		}
	}
}

func transformTelemetryValue(event ConfigEvent) (any, bool) {
	if event.Kind != EventConfiguration {
		return nil, true
	}
	if !event.ReportValue {
		return nil, false
	}
	switch event.Policy {
	case TelemetryReport:
		value, err := prepareConfigTelemetryValue(event.Value)
		if err != nil {
			log.Warn("config: unable to prepare %s telemetry: %v", event.Name, err)
			return nil, false
		}
		return value, true
	case TelemetryRedact:
		return redactedTelemetryValue, true
	case TelemetrySanitizeURL:
		raw, ok := event.Value.(string)
		if !ok {
			return nil, false
		}
		return urlsanitizer.SanitizeURL(raw), true
	default:
		return nil, false
	}
}

func telemetryStateHash(event ConfigEvent, value any) [sha256.Size]byte {
	state := fmt.Sprintf("%T:%#v|origin:%s|id:%s|present:%t|valid:%t|report:%t",
		value, value, event.Origin, event.ConfigID, event.Present, event.Valid, event.ReportValue)
	return sha256.Sum256([]byte(state))
}

func submitReportAction(action reportAction) {
	event := action.event
	switch event.Kind {
	case EventConfiguration:
		action.prepared.Submit(action.value)
	case EventOTelEnvHiding:
		reportOTelDiagnostic("otel.env.hiding", event.Name, event.OTelName)
	case EventOTelEnvInvalid:
		reportOTelDiagnostic("otel.env.invalid", event.Name, event.OTelName)
	}
}

func reportOTelDiagnostic(metric, ddKey, otelKey string) {
	tags := []string{
		"config_datadog:" + strings.ToLower(ddKey),
		"config_opentelemetry:" + strings.ToLower(otelKey),
	}
	telemetry.Count(telemetry.NamespaceTracers, metric, tags).Submit(1)
}

// ResetForTesting clears all deduplication state.
func (r *Reporter) ResetForTesting() {
	r.mu.Lock()
	clear(r.generation)
	clear(r.once)
	clear(r.changes)
	r.mu.Unlock()
}

func (r *Reporter) stateSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.once) + len(r.changes)
}
