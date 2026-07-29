// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func newTestReporter(t *testing.T) (*Reporter, *telemetrytest.RecordClient) {
	t.Helper()
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	return newReporter(), rec
}

func newZeroValueTestReporter(t *testing.T) (*Reporter, *telemetrytest.RecordClient) {
	t.Helper()
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	return new(Reporter), rec
}

func newTestReporterWithDefinitions(
	t *testing.T,
	raw []RawDefinition,
	bindings []ConsumerBinding,
) (*Reporter, *telemetrytest.RecordClient) {
	t.Helper()
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	return newReporterWithDefinitions(raw, bindings), rec
}

func newPerCallTestReporter(t *testing.T, name string) (*Reporter, *telemetrytest.RecordClient, string) {
	t.Helper()
	bindingID := "test.per-call." + name
	r, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{{Key: name, Telemetry: TelemetryReport}},
		[]ConsumerBinding{{ID: bindingID, Consumer: "test", Keys: []string{name}, Sampling: SamplePerCall}},
	)
	return r, rec, bindingID
}

func configEvent(value any, policy TelemetryPolicy, cadence ReportCadence) ConfigEvent {
	return ConfigEvent{
		Kind:          EventConfiguration,
		BindingID:     "tracer.DD_SERVICE",
		Name:          "DD_SERVICE",
		Value:         value,
		Present:       true,
		Valid:         true,
		Origin:        telemetry.OriginEnvVar,
		Policy:        policy,
		Cadence:       cadence,
		ReportValue:   true,
		SourceOrdinal: 1,
	}
}

func largeBackingSubstring(value string) string {
	backing := strings.Repeat("x", 16<<20) + value
	return backing[len(backing)-len(value):]
}

func stringDataPointer(value string) uintptr {
	return uintptr(unsafe.Pointer(unsafe.StringData(value)))
}

func TestZeroValueReporterIndexesAcceptedBindingsLazily(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	require.Empty(t, r.bindings)
	require.Empty(t, r.generation)
	require.Empty(t, r.once)
	require.Empty(t, r.changes)

	service := configEvent("service", TelemetryReport, ReportOncePerGeneration)
	r.Report([]ConfigEvent{service}, 1)

	require.Len(t, rec.Configuration, 1, "the first accepted event must report immediately")
	require.Len(t, r.bindings, 1)
	require.Contains(t, r.bindings, service.BindingID)

	version := configEvent("version", TelemetryReport, ReportOncePerGeneration)
	version.BindingID = "tracer.DD_VERSION"
	version.Name = "DD_VERSION"
	r.Report([]ConfigEvent{version}, 1)

	require.Len(t, rec.Configuration, 2)
	require.Len(t, r.bindings, 2)
	require.Contains(t, r.bindings, version.BindingID)
}

func TestConstructedReportersRemainComplete(t *testing.T) {
	_, registeredBindings := RegisteredDefinitions()
	eager := NewReporter()
	require.Len(t, eager.bindings, len(registeredBindings))

	custom, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{{Key: "DD_TEST_CUSTOM", Telemetry: TelemetryReport}},
		[]ConsumerBinding{{
			ID: "test.custom", Consumer: "test",
			Keys: []string{"DD_TEST_CUSTOM"}, Sampling: SampleTracerConstruction,
		}},
	)
	custom.Report([]ConfigEvent{configEvent(
		"global", TelemetryReport, ReportOncePerGeneration,
	)}, 1)

	require.Empty(t, rec.Configuration,
		"a custom-definition reporter must not fall back to global bindings")
	require.Len(t, custom.bindings, 1)
}

func TestZeroValueReporterRejectsAdversarialEventsWithoutState(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	unsupported := &reporterGoStringer{reporter: r, called: make(chan struct{})}
	for i := 0; i < 1000; i++ {
		events := []ConfigEvent{
			configEvent("unknown", TelemetryReport, ReportOncePerGeneration),
			configEvent("bad-name", TelemetryReport, ReportOncePerGeneration),
			configEvent("bad-policy", TelemetryRedact, ReportOncePerGeneration),
			configEvent("bad-cadence", TelemetryReport, ReportOnChange),
			configEvent("never", TelemetryReport, ReportNever),
			configEvent("bad-origin", TelemetryReport, ReportOncePerGeneration),
			configEvent("bad-ordinal", TelemetryReport, ReportOncePerGeneration),
			configEvent("bad-kind", TelemetryReport, ReportOncePerGeneration),
			configEvent("omit", TelemetryOmit, ReportOncePerGeneration),
		}
		events[0].BindingID = fmt.Sprintf("unknown-%d", i)
		events[1].Name = fmt.Sprintf("DD_UNKNOWN_%d", i)
		events[5].Origin = telemetry.Origin(fmt.Sprintf("unknown-%d", i))
		events[6].SourceOrdinal = schema.SourceOrdinalMax + 1
		events[7].Kind = EventKind(255)
		events[8].BindingID = "tracer.DD_TAGS"
		events[8].Name = "DD_TAGS"
		r.Report(events, uint64(i+1))
	}
	r.Report([]ConfigEvent{
		configEvent(unsupported, TelemetryReport, ReportOncePerGeneration),
	}, 1001)
	absent := configEvent(nil, TelemetryReport, ReportOnChange)
	absent.BindingID = "system.hostname"
	absent.Name = "DD_HOSTNAME"
	absent.Present = false
	absent.Valid = false
	absent.ReportValue = false
	r.Report([]ConfigEvent{absent}, 1002)

	require.Empty(t, rec.Configuration)
	require.Empty(t, r.bindings)
	require.Empty(t, r.generation)
	require.Empty(t, r.once)
	require.Empty(t, r.changes)
	select {
	case <-unsupported.called:
		t.Fatal("unsupported pointer value invoked a formatting callback")
	default:
	}
}

func TestZeroValueReporterValidatesMetadataBeforeTransforming(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()

	unknown := configEvent(new(int), TelemetryReport, ReportOncePerGeneration)
	unknown.BindingID = "unknown.binding"
	mismatched := configEvent(new(int), TelemetryReport, ReportOnChange)

	r.Report([]ConfigEvent{unknown, mismatched}, 1)

	require.Empty(t, logger.Logs(),
		"rejected metadata must not reach value transformation or warning paths")
	require.Empty(t, rec.Configuration)
	require.Empty(t, r.bindings)
	require.Empty(t, r.generation)
	require.Empty(t, r.once)
	require.Empty(t, r.changes)
}

func TestZeroValueReporterCanonicalizesCallerBackedStrings(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	canonicalBindingID := definitionsRegistry.bindings[0].ID
	canonicalName := definitionsRegistry.bindings[0].Keys[0]
	for _, binding := range definitionsRegistry.bindings {
		if binding.ID == "tracer.DD_SERVICE" {
			canonicalBindingID = binding.ID
			canonicalName = binding.Keys[0]
			break
		}
	}

	event := configEvent("service", TelemetryReport, ReportOncePerGeneration)
	event.BindingID = largeBackingSubstring(canonicalBindingID)
	event.Name = largeBackingSubstring(canonicalName)
	event.Origin = telemetry.Origin(largeBackingSubstring(string(telemetry.OriginManagedStableConfig)))
	event.ConfigID = largeBackingSubstring("config-123")
	require.NotEqual(t, stringDataPointer(canonicalBindingID), stringDataPointer(event.BindingID))
	require.NotEqual(t, stringDataPointer(canonicalName), stringDataPointer(event.Name))
	require.NotEqual(t, stringDataPointer(string(telemetry.OriginManagedStableConfig)), stringDataPointer(string(event.Origin)))

	r.Report([]ConfigEvent{event}, 1)

	require.Len(t, rec.Configuration, 1)
	require.Equal(t, canonicalName, rec.Configuration[0].Name)
	require.Equal(t, telemetry.OriginManagedStableConfig, rec.Configuration[0].Origin)
	require.Equal(t, "config-123", rec.Configuration[0].ID)
	require.Equal(t, stringDataPointer(canonicalName), stringDataPointer(rec.Configuration[0].Name),
		"the buffered action must use the registry-owned name")
	require.Equal(t,
		stringDataPointer(string(telemetry.OriginManagedStableConfig)),
		stringDataPointer(string(rec.Configuration[0].Origin)),
		"the buffered action must use the canonical origin constant",
	)
	require.NotEqual(t, stringDataPointer(event.ConfigID), stringDataPointer(rec.Configuration[0].ID),
		"the buffered action must detach a small config ID from its large caller backing")

	require.Len(t, r.generation, 1)
	for bindingID := range r.generation {
		require.Equal(t, stringDataPointer(canonicalBindingID), stringDataPointer(bindingID))
	}
	require.Len(t, r.once, 1)
	for key := range r.once {
		require.Equal(t, stringDataPointer(canonicalBindingID), stringDataPointer(key.bindingID))
		require.Equal(t, stringDataPointer(canonicalName), stringDataPointer(key.name))
	}
}

func TestZeroValueReporterPreservesGenerationAndOnChange(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	service := configEvent("first", TelemetryReport, ReportOncePerGeneration)

	r.Report([]ConfigEvent{service}, 2)
	r.Report([]ConfigEvent{service}, 1)
	service.Value = "second"
	r.Report([]ConfigEvent{service}, 3)

	hostname := configEvent("one", TelemetryReport, ReportOnChange)
	hostname.BindingID = "system.hostname"
	hostname.Name = "DD_HOSTNAME"
	r.Report([]ConfigEvent{hostname}, 1)
	r.Report([]ConfigEvent{hostname}, 2)
	hostname.Value = "two"
	r.Report([]ConfigEvent{hostname}, 3)

	require.Len(t, rec.Configuration, 4)
	require.Equal(t, []any{"first", "second", "one", "two"}, []any{
		rec.Configuration[0].Value,
		rec.Configuration[1].Value,
		rec.Configuration[2].Value,
		rec.Configuration[3].Value,
	})
	require.Len(t, r.bindings, 2)
	require.Len(t, r.generation, 2)
	require.Len(t, r.once, 1)
	require.Len(t, r.changes, 1)
}

func TestZeroValueReporterConcurrentFirstUse(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	service := configEvent("service", TelemetryReport, ReportOncePerGeneration)
	version := configEvent("version", TelemetryReport, ReportOncePerGeneration)
	version.BindingID = "tracer.DD_VERSION"
	version.Name = "DD_VERSION"
	hostname := configEvent("host", TelemetryReport, ReportOnChange)
	hostname.BindingID = "system.hostname"
	hostname.Name = "DD_HOSTNAME"

	const goroutines = 96
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		event, generation := service, uint64(2)
		switch i % 3 {
		case 1:
			event = version
			generation = 1
		case 2:
			event = hostname
			generation = 3
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r.Report([]ConfigEvent{event}, generation)
		}()
	}
	close(start)
	wg.Wait()

	require.Len(t, rec.Configuration, 3)
	require.Len(t, r.bindings, 3)
	require.Len(t, r.generation, 3)
	require.Len(t, r.once, 2)
	require.Len(t, r.changes, 1)

	currentService := service
	currentService.Value = "current-service"
	r.Report([]ConfigEvent{currentService}, 10)
	currentHostname := hostname
	currentHostname.Value = "current-host"
	r.Report([]ConfigEvent{currentHostname}, 10)
	require.Len(t, rec.Configuration, 5)

	start = make(chan struct{})
	for i := 0; i < goroutines; i++ {
		event, generation := currentService, uint64(10)
		switch i % 4 {
		case 1:
			event.Value = "stale-service"
			generation = 9
		case 2:
			event = currentHostname
		case 3:
			event = currentHostname
			event.Value = "stale-host"
			generation = 9
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r.Report([]ConfigEvent{event}, generation)
		}()
	}
	close(start)
	wg.Wait()

	require.Len(t, rec.Configuration, 5,
		"concurrent duplicate and stale generations must not reopen once/change state")
	require.Equal(t, uint64(10), r.generation[service.BindingID])
	require.Equal(t, uint64(10), r.generation[hostname.BindingID])
}

func TestZeroValueReporterReportsPackageInitEventImmediatelyOnce(t *testing.T) {
	r, rec := newZeroValueTestReporter(t)
	event := configEvent("false", TelemetryReport, ReportOncePerGeneration)
	event.BindingID = "tracer.seelog-init"
	event.Name = "DD_TRACE_DEBUG_SEELOG_WORKAROUND"

	r.Report([]ConfigEvent{event, event}, 1)

	require.Len(t, rec.Configuration, 1)
	require.Equal(t, "false", rec.Configuration[0].Value)
	require.Len(t, r.bindings, 1)
}

func TestReporterDropsDisabledWithoutState(t *testing.T) {
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	r := newReporter()

	r.Report([]ConfigEvent{configEvent("value", TelemetryReport, ReportOncePerGeneration)}, 1)

	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())
}

func TestReporterOncePerGenerationAndEvictsOldGenerations(t *testing.T) {
	r, rec := newTestReporter(t)
	event := configEvent("value", TelemetryReport, ReportOncePerGeneration)

	r.Report([]ConfigEvent{event, event}, 1)
	r.Report([]ConfigEvent{event}, 1)
	require.Len(t, rec.Configuration, 1)

	r.Report([]ConfigEvent{event}, 2)
	require.Len(t, rec.Configuration, 2)
	require.Equal(t, 1, r.stateSize())

	r.Report([]ConfigEvent{event}, 1)
	require.Len(t, rec.Configuration, 2, "stale generations must be dropped")
}

func TestReporterGenerationsAreIndependentPerBinding(t *testing.T) {
	r, rec := newTestReporter(t)
	service := configEvent("service", TelemetryReport, ReportOncePerGeneration)
	version := configEvent("version", TelemetryReport, ReportOncePerGeneration)
	version.BindingID = "tracer.DD_VERSION"
	version.Name = "DD_VERSION"

	r.Report([]ConfigEvent{service}, 2)
	r.Report([]ConfigEvent{version}, 1)

	require.Len(t, rec.Configuration, 2)
}

func TestReporterOnChange(t *testing.T) {
	r, rec, bindingID := newPerCallTestReporter(t, "DD_TEST_ON_CHANGE")
	event := configEvent("one", TelemetryReport, ReportOnChange)
	event.BindingID = bindingID
	event.Name = "DD_TEST_ON_CHANGE"

	r.Report([]ConfigEvent{event}, 1)
	r.Report([]ConfigEvent{event}, 2)
	event.Value = "two"
	r.Report([]ConfigEvent{event}, 3)

	require.Len(t, rec.Configuration, 2)
	assert.Equal(t, "one", rec.Configuration[0].Value)
	assert.Equal(t, "two", rec.Configuration[1].Value)
	require.Equal(t, 1, r.stateSize())
}

func TestReporterOnChangeTracksNonReportableStateTransitions(t *testing.T) {
	const (
		bindingID = "test.per-call"
		name      = "DD_TEST_PER_CALL"
	)
	r, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{{Key: name, Telemetry: TelemetryReport}},
		[]ConsumerBinding{{ID: bindingID, Consumer: "test", Keys: []string{name}, Sampling: SamplePerCall}},
	)
	valid := configEvent("same", TelemetryReport, ReportOnChange)
	valid.BindingID = bindingID
	valid.Name = name
	absent := valid
	absent.Value = "SENTINEL-ABSENT-RAW"
	absent.Present = false
	absent.Valid = false
	absent.ReportValue = false

	r.Report([]ConfigEvent{valid}, 1)
	r.Report([]ConfigEvent{absent}, 2)
	r.Report([]ConfigEvent{valid}, 3)

	require.Len(t, rec.Configuration, 2)
	assert.Equal(t, "same", rec.Configuration[0].Value)
	assert.Equal(t, "same", rec.Configuration[1].Value)
	require.Equal(t, 1, r.stateSize())
	require.NotContains(t, fmt.Sprint(r), "SENTINEL-ABSENT-RAW")
}

func TestReporterOnceNonReportableDoesNotConsumeSlot(t *testing.T) {
	r, rec := newTestReporter(t)
	absent := configEvent("SENTINEL-ABSENT-RAW", TelemetryReport, ReportOncePerGeneration)
	absent.Present = false
	absent.Valid = false
	absent.ReportValue = false
	valid := absent
	valid.Value = "reported"
	valid.Present = true
	valid.Valid = true
	valid.ReportValue = true

	r.Report([]ConfigEvent{absent}, 1)
	r.Report([]ConfigEvent{valid}, 1)

	require.Len(t, rec.Configuration, 1)
	require.Equal(t, "reported", rec.Configuration[0].Value)
	require.Equal(t, 1, r.stateSize())
	require.NotContains(t, fmt.Sprint(r), "SENTINEL-ABSENT-RAW")
}

func TestReporterNeverAndOmitDoNotMutateGeneration(t *testing.T) {
	r, rec := newTestReporter(t)
	never := configEvent("SENTINEL-NEVER", TelemetryReport, ReportNever)
	omit := configEvent("SENTINEL-OMIT", TelemetryOmit, ReportOncePerGeneration)
	omit.BindingID = "tracer.DD_TAGS"
	omit.Name = "DD_TAGS"

	r.Report([]ConfigEvent{never, omit}, 99)

	require.Empty(t, rec.Configuration)
	require.Empty(t, r.generation)
	require.Zero(t, r.stateSize())
	require.NotContains(t, fmt.Sprint(r), "SENTINEL")
}

func TestReporterOnChangeConfigIDsRemainBounded(t *testing.T) {
	r, rec, bindingID := newPerCallTestReporter(t, "DD_TEST_CONFIG_ID")
	event := configEvent("value", TelemetryReport, ReportOnChange)
	event.BindingID = bindingID
	event.Name = "DD_TEST_CONFIG_ID"
	for i := 0; i < 1000; i++ {
		event.ConfigID = fmt.Sprintf("config-%d", i)
		r.Report([]ConfigEvent{event}, uint64(i+1))
	}

	require.Len(t, rec.Configuration, 1000)
	require.Equal(t, 1, r.stateSize())
	require.NotContains(t, fmt.Sprint(r), "config-999")
}

func TestReporterNeverAndOmitStoreNothing(t *testing.T) {
	r, rec := newTestReporter(t)
	never := configEvent("secret-never", TelemetryReport, ReportNever)
	omit := configEvent("secret-omit", TelemetryOmit, ReportOncePerGeneration)

	r.Report([]ConfigEvent{never, omit}, 1)

	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())
	require.NotContains(t, fmt.Sprint(r), "secret-never")
	require.NotContains(t, fmt.Sprint(r), "secret-omit")
}

func TestReporterPolicies(t *testing.T) {
	r, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{
			{Key: "DD_TEST_REPORT", Telemetry: TelemetryReport},
			{Key: "DD_TEST_REDACT", Telemetry: TelemetryRedact},
			{Key: "DD_TEST_URL", Telemetry: TelemetrySanitizeURL},
		},
		[]ConsumerBinding{
			{ID: "test.report", Consumer: "test", Keys: []string{"DD_TEST_REPORT"}, Sampling: SampleTracerConstruction},
			{ID: "test.redact", Consumer: "test", Keys: []string{"DD_TEST_REDACT"}, Sampling: SampleTracerConstruction},
			{ID: "test.url", Consumer: "test", Keys: []string{"DD_TEST_URL"}, Sampling: SampleTracerConstruction},
		},
	)
	report := configEvent("plain", TelemetryReport, ReportOncePerGeneration)
	report.BindingID = "test.report"
	report.Name = "DD_TEST_REPORT"
	redact := configEvent("top-secret", TelemetryRedact, ReportOncePerGeneration)
	redact.BindingID = "test.redact"
	redact.Name = "DD_TEST_REDACT"
	sanitized := configEvent("https://user:password@example.com/repo.git", TelemetrySanitizeURL, ReportOncePerGeneration)
	sanitized.BindingID = "test.url"
	sanitized.Name = "DD_TEST_URL"

	r.Report([]ConfigEvent{report, redact, sanitized}, 1)

	require.Len(t, rec.Configuration, 3)
	assert.Equal(t, "plain", rec.Configuration[0].Value)
	assert.Equal(t, redactedTelemetryValue, rec.Configuration[1].Value)
	gotURL := fmt.Sprint(rec.Configuration[2].Value)
	assert.NotContains(t, gotURL, "user")
	assert.NotContains(t, gotURL, "password")
	assert.Contains(t, gotURL, "example.com/repo.git")
	state := fmt.Sprint(r)
	assert.NotContains(t, state, "top-secret")
	assert.NotContains(t, state, "user")
	assert.NotContains(t, state, "password")
}

func TestReporterConfigurationDefaultAndConfigID(t *testing.T) {
	r, rec := newTestReporter(t)
	def := configEvent("default", TelemetryReport, ReportOncePerGeneration)
	def.Origin = telemetry.OriginDefault
	def.SourceOrdinal = 4
	fromFile := configEvent("managed", TelemetryReport, ReportOncePerGeneration)
	fromFile.Origin = telemetry.OriginManagedStableConfig
	fromFile.ConfigID = "config-123"
	fromFile.SourceOrdinal = 0

	r.Report([]ConfigEvent{def, fromFile}, 1)

	require.Len(t, rec.Configuration, 2)
	assert.Equal(t, uint64(1), rec.Configuration[0].SeqID)
	assert.Equal(t, telemetry.EmptyID, rec.Configuration[0].ID)
	assert.Equal(t, "config-123", rec.Configuration[1].ID)
	assert.Greater(t, rec.Configuration[1].SeqID, rec.Configuration[0].SeqID)
}

func TestReporterSourceOrdinalAndKindDoNotCollide(t *testing.T) {
	r, rec := newTestReporter(t)
	dd := configEvent("dd", TelemetryReport, ReportOncePerGeneration)
	dd.SourceOrdinal = 1
	otel := dd
	otel.Value = "otel"
	otel.SourceOrdinal = 2
	diagnostic := dd
	diagnostic.Kind = EventOTelEnvHiding
	diagnostic.OTelName = "OTEL_SERVICE_NAME"
	diagnostic.Policy = TelemetryReport
	diagnostic.CompatibilityReport = false

	r.Report([]ConfigEvent{dd, otel, diagnostic}, 1)

	require.Len(t, rec.Configuration, 2)
	require.Len(t, rec.Metrics, 1, "diagnostics dispatch by kind, not CompatibilityReport")
}

func TestReporterBoundsUnknownBindingsAndManyGenerations(t *testing.T) {
	r, rec := newTestReporter(t)
	for i := 0; i < 1000; i++ {
		unknown := configEvent(strings.Repeat("secret", 20), TelemetryReport, ReportOncePerGeneration)
		unknown.BindingID = fmt.Sprintf("unknown-%d", i)
		r.Report([]ConfigEvent{unknown}, uint64(i+1))
	}
	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())

	event := configEvent("value", TelemetryReport, ReportOncePerGeneration)
	for i := 0; i < 1000; i++ {
		r.Report([]ConfigEvent{event}, uint64(i+1))
	}
	require.LessOrEqual(t, r.stateSize(), 1)
}

func TestReporterBoundsAdversarialFieldsForRegisteredBinding(t *testing.T) {
	r, rec := newTestReporter(t)
	for i := 0; i < 1000; i++ {
		event := configEvent("value", TelemetryReport, ReportOncePerGeneration)
		event.Name = fmt.Sprintf("DD_ADVERSARIAL_%d", i)
		event.SourceOrdinal = uint16(i)
		event.Origin = telemetry.Origin(fmt.Sprintf("origin-%d", i))
		r.Report([]ConfigEvent{event}, 1)
	}

	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())

	for i := 0; i < 1000; i++ {
		event := configEvent("value", TelemetryReport, ReportOncePerGeneration)
		event.SourceOrdinal = uint16(i + int(schema.SourceOrdinalMax) + 1)
		event.Origin = telemetry.Origin(fmt.Sprintf("origin-%d", i))
		r.Report([]ConfigEvent{event}, 1)
	}
	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())

	for i := 0; i < 1000; i++ {
		event := configEvent("value", TelemetryReport, ReportOnChange)
		event.Origin = telemetry.Origin(fmt.Sprintf("origin-%d", i))
		r.Report([]ConfigEvent{event}, uint64(i+1))
	}
	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())
}

func TestReporterRejectsForgedRegisteredTelemetryPolicyWithoutState(t *testing.T) {
	r, rec := newTestReporter(t)
	for _, tc := range []struct {
		bindingID string
		name      string
	}{
		{bindingID: "tracer.DD_TAGS", name: "DD_TAGS"},
		{bindingID: "tracer.DD_TRACE_FEATURES", name: "DD_TRACE_FEATURES"},
	} {
		event := configEvent("SENTINEL-SECRET", TelemetryReport, ReportOncePerGeneration)
		event.BindingID = tc.bindingID
		event.Name = tc.name

		r.Report([]ConfigEvent{event}, 99)
	}

	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())
	require.Empty(t, r.generation)
	require.NotContains(t, fmt.Sprint(r), "SENTINEL-SECRET")
}

func TestReporterRejectsUnknownOrMismatchedCadenceBeforeGenerationMutation(t *testing.T) {
	r, rec := newTestReporter(t)
	valid := configEvent("first", TelemetryReport, ReportOncePerGeneration)
	r.Report([]ConfigEvent{valid}, 1)
	require.Len(t, rec.Configuration, 1)
	require.Equal(t, uint64(1), r.generation[valid.BindingID])

	unknown := valid
	unknown.Cadence = ReportCadence(255)
	r.Report([]ConfigEvent{unknown}, 2)

	mismatch := valid
	mismatch.Cadence = ReportOnChange
	r.Report([]ConfigEvent{mismatch}, 3)

	require.Len(t, rec.Configuration, 1)
	require.Equal(t, uint64(1), r.generation[valid.BindingID])
	require.Equal(t, 1, r.stateSize(), "invalid cadence must not evict once-per-generation state")

	r.Report([]ConfigEvent{valid}, 1)
	require.Len(t, rec.Configuration, 1, "the original generation must remain deduplicated")
}

func TestReporterResetForTesting(t *testing.T) {
	r, rec := newTestReporter(t)
	event := configEvent("value", TelemetryReport, ReportOncePerGeneration)
	r.Report([]ConfigEvent{event}, 1)
	require.NotZero(t, r.stateSize())

	r.ResetForTesting()
	require.Zero(t, r.stateSize())
	r.Report([]ConfigEvent{event}, 1)
	require.Len(t, rec.Configuration, 2)
}

func TestReporterSanitizeURLRemovesSCPUserinfo(t *testing.T) {
	const (
		bindingID = "test.scp-url"
		name      = "DD_TEST_SCP_URL"
	)
	r, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{{Key: name, Telemetry: TelemetrySanitizeURL}},
		[]ConsumerBinding{{ID: bindingID, Consumer: "test", Keys: []string{name}, Sampling: SampleTracerConstruction}},
	)
	for i, raw := range []string{
		"token@github.com:org/repo.git",
		"username:password@github.com:org/repo.git",
	} {
		event := configEvent(raw, TelemetrySanitizeURL, ReportOncePerGeneration)
		event.BindingID = bindingID
		event.Name = name
		r.Report([]ConfigEvent{event}, uint64(i+1))

		require.Len(t, rec.Configuration, i+1)
		sanitized := fmt.Sprint(rec.Configuration[i].Value)
		require.NotContains(t, sanitized, "token")
		require.NotContains(t, sanitized, "username")
		require.NotContains(t, sanitized, "password")
		require.Contains(t, sanitized, "github.com:org/repo.git")
	}
	require.NotContains(t, fmt.Sprint(r), "token")
	require.NotContains(t, fmt.Sprint(r), "password")
}

type reentrantConfigClient struct {
	*telemetrytest.RecordClient
	reporter *Reporter
	event    ConfigEvent
}

func (c *reentrantConfigClient) RegisterAppConfigs(kvs ...telemetry.Configuration) {
	c.RecordClient.RegisterAppConfigs(kvs...)
	c.reporter.Report([]ConfigEvent{c.event}, 1)
}

type reporterGoStringer struct {
	reporter *Reporter
	called   chan struct{}
	once     sync.Once
}

func (s *reporterGoStringer) GoString() string {
	_ = s.reporter.stateSize()
	s.once.Do(func() { close(s.called) })
	return "safe"
}

type mutateBeforeRecordClient struct {
	*telemetrytest.RecordClient
	original []string
}

func (c *mutateBeforeRecordClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.original[0] = "mutated"
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestReporterSnapshotsMutableValuesBeforeSink(t *testing.T) {
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	value := []string{"original"}
	client := &mutateBeforeRecordClient{
		RecordClient: new(telemetrytest.RecordClient),
		original:     value,
	}
	t.Cleanup(telemetry.MockClient(client))
	const name = "DD_TEST_SLICE_SNAPSHOT"
	r := newReporterWithDefinitions(
		[]RawDefinition{{Key: name, Telemetry: TelemetryReport}},
		[]ConsumerBinding{{ID: "test.slice-snapshot", Consumer: "test", Keys: []string{name}, Sampling: SamplePerCall}},
	)
	event := configEvent(value, TelemetryReport, ReportOnChange)
	event.BindingID = "test.slice-snapshot"
	event.Name = name

	r.Report([]ConfigEvent{event}, 1)

	require.Len(t, client.Configuration, 1)
	require.Equal(t, "original", client.Configuration[0].Value)
}

type mutateBytesBeforeRecordClient struct {
	*telemetrytest.RecordClient
	original []byte
}

func (c *mutateBytesBeforeRecordClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.original[0] = 'X'
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestReporterSnapshotsBytesBeforeSink(t *testing.T) {
	const (
		bindingID = "test.per-call-bytes"
		name      = "DD_TEST_PER_CALL_BYTES"
	)
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	value := []byte("original")
	client := &mutateBytesBeforeRecordClient{
		RecordClient: new(telemetrytest.RecordClient),
		original:     value,
	}
	t.Cleanup(telemetry.MockClient(client))
	r := newReporterWithDefinitions(
		[]RawDefinition{{Key: name, Telemetry: TelemetryReport}},
		[]ConsumerBinding{{ID: bindingID, Consumer: "test", Keys: []string{name}, Sampling: SamplePerCall}},
	)
	event := configEvent(value, TelemetryReport, ReportOnChange)
	event.BindingID = bindingID
	event.Name = name

	r.Report([]ConfigEvent{event}, 1)

	require.Len(t, client.Configuration, 1)
	require.Equal(t, "[111 114 105 103 105 110 97 108]", client.Configuration[0].Value)
}

func TestReporterDropsUnsupportedMutableValue(t *testing.T) {
	const (
		bindingID = "test.per-call-pointer"
		name      = "DD_TEST_PER_CALL_POINTER"
	)
	r, rec := newTestReporterWithDefinitions(t,
		[]RawDefinition{{Key: name, Telemetry: TelemetryReport}},
		[]ConsumerBinding{{ID: bindingID, Consumer: "test", Keys: []string{name}, Sampling: SamplePerCall}},
	)
	value := &reporterGoStringer{reporter: r, called: make(chan struct{})}
	event := configEvent(value, TelemetryReport, ReportOnChange)
	event.BindingID = bindingID
	event.Name = name

	r.Report([]ConfigEvent{event}, 1)

	require.Empty(t, rec.Configuration)
	require.Zero(t, r.stateSize())
	select {
	case <-value.called:
		t.Fatal("unsupported pointer value invoked a formatting callback")
	default:
	}
}

func TestReporterRejectsMalformedOTelDiagnosticName(t *testing.T) {
	r, rec := newTestReporter(t)
	event := configEvent(nil, TelemetryReport, ReportOncePerGeneration)
	event.Kind = EventOTelEnvInvalid
	event.OTelName = "OTEL_SECRET_VALUE"

	r.Report([]ConfigEvent{event}, 1)

	require.Empty(t, rec.Metrics)
	require.Zero(t, r.stateSize())
}

func TestReporterDoesNotHoldLockWhileCallingSink(t *testing.T) {
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	r := new(Reporter)
	reentered := configEvent("reentered", TelemetryReport, ReportOncePerGeneration)
	reentered.BindingID = "tracer.DD_VERSION"
	reentered.Name = "DD_VERSION"
	client := &reentrantConfigClient{
		RecordClient: new(telemetrytest.RecordClient),
		reporter:     r,
		event:        reentered,
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Report([]ConfigEvent{configEvent("outer", TelemetryReport, ReportOncePerGeneration)}, 1)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reporter deadlocked while the sink reentered Report")
	}
	require.Len(t, client.Configuration, 2)
}

func TestReporterSequenceFollowsAcceptedGenerationWhenSinkArrivalInverts(t *testing.T) {
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	r := new(Reporter)
	second := configEvent("second", TelemetryReport, ReportOncePerGeneration)
	client := &reentrantOrderingClient{RecordClient: new(telemetrytest.RecordClient)}
	client.onFirst = func() {
		r.Report([]ConfigEvent{second}, 2)
	}
	t.Cleanup(telemetry.MockClient(client))

	first := configEvent("first", TelemetryReport, ReportOncePerGeneration)
	r.Report([]ConfigEvent{first}, 1)

	require.Len(t, client.Configuration, 2)
	require.Equal(t, "second", client.Configuration[0].Value, "newer generation reaches the sink first")
	require.Equal(t, "first", client.Configuration[1].Value, "older generation reaches the sink last")
	require.Less(t, client.Configuration[1].SeqID, client.Configuration[0].SeqID,
		"sequence order follows accepted generation order despite inverted arrival")
}
