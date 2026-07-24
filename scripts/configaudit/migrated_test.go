// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var migratedInventory = []string{
	"DD_AGENT_HOST",
	"DD_API_KEY",
	"DD_APP_KEY",
	"DD_CIVISIBILITY_AGENTLESS_ENABLED",
	"DD_CIVISIBILITY_AGENTLESS_URL",
	"DD_CIVISIBILITY_ENABLED",
	"DD_CIVISIBILITY_USE_NOOP_TRACER",
	"DD_DATA_STREAMS_ENABLED",
	"DD_DOGSTATSD_HOST",
	"DD_DOGSTATSD_PORT",
	"DD_DOGSTATSD_URL",
	"DD_DYNAMIC_INSTRUMENTATION_ENABLED",
	"DD_ENV",
	"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED",
	"DD_LOGS_OTEL_ENABLED",
	"DD_METRICS_OTEL_ENABLED",
	"DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED",
	"DD_PROFILING_ENDPOINT_COLLECTION_ENABLED",
	"DD_RUNTIME_METRICS_ENABLED",
	"DD_RUNTIME_METRICS_V2_ENABLED",
	"DD_SERVICE",
	"DD_SERVICE_MAPPING",
	"DD_SITE",
	"DD_TAGS",
	"DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED",
	"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED",
	"DD_TRACE_ABANDONED_SPAN_TIMEOUT",
	"DD_TRACE_AGENT_PORT",
	"DD_TRACE_AGENT_PROTOCOL_VERSION",
	"DD_TRACE_AGENT_TIMEOUT",
	"DD_TRACE_AGENT_URL",
	"DD_TRACE_ANALYTICS_ENABLED",
	"DD_TRACE_DEBUG",
	"DD_TRACE_DEBUG_ABANDONED_SPANS",
	"DD_TRACE_DEBUG_STACK",
	"DD_TRACE_ENABLED",
	"DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED",
	"DD_TRACE_FEATURES",
	"DD_TRACE_INTERNAL_METRICS_ENABLED",
	"DD_TRACE_LOG_DIRECTORY",
	"DD_TRACE_OTEL_SEMANTICS_ENABLED",
	"DD_TRACE_PARTIAL_FLUSH_ENABLED",
	"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS",
	"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED",
	"DD_TRACE_PEER_SERVICE_MAPPING",
	"DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT",
	"DD_TRACE_PROPAGATION_EXTRACT_FIRST",
	"DD_TRACE_PROPAGATION_STYLE_EXTRACT",
	"DD_TRACE_PROPAGATION_STYLE_INJECT",
	"DD_TRACE_RATE_LIMIT",
	"DD_TRACE_REPORT_HOSTNAME",
	"DD_TRACE_RETRY_INTERVAL",
	"DD_TRACE_SAMPLE_RATE",
	"DD_TRACE_SEND_RETRIES",
	"DD_TRACE_SOURCE_HOSTNAME",
	"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA",
	"DD_TRACE_STARTUP_LOGS",
	"DD_TRACE_STATS_ADDITIONAL_TAGS",
	"DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT",
	"DD_TRACE_STATS_CARDINALITY_LIMIT",
	"DD_TRACE_STATS_COMPUTATION_ENABLED",
	"DD_TRACE_STATS_HTTP_ENDPOINT_CARDINALITY_LIMIT",
	"DD_TRACE_STATS_ORIGIN_CARDINALITY_LIMIT",
	"DD_TRACE_STATS_PEER_TAGS_CARDINALITY_LIMIT",
	"DD_TRACE_STATS_RESOURCE_CARDINALITY_LIMIT",
	"DD_TRACE_UNIVERSAL_VERSION_ENABLED",
	"DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH",
	"DD_VERSION",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
	"OTEL_LOGS_EXPORTER",
	"OTEL_METRICS_EXPORTER",
	"OTEL_TRACES_EXPORTER",
	"OTEL_TRACES_SPAN_METRICS_ENABLED",
}

func TestLoadMigrated_RealRepo(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatalf("loadMigrated: %v", err)
	}
	if len(got) != len(migratedInventory) {
		t.Fatalf("got %d migrated keys, want %d", len(got), len(migratedInventory))
	}
	for _, key := range migratedInventory {
		if _, ok := got[key]; !ok {
			t.Errorf("expected %s in migrated set", key)
		}
	}
	// DD_APPSEC_ENABLED is registered but not migrated to internal/config.
	if _, ok := got["DD_APPSEC_ENABLED"]; ok {
		t.Errorf("did not expect DD_APPSEC_ENABLED to be in migrated set yet")
	}
}

func TestLoadMigrated_ResolvesPackageConstants(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	// CIVisibilityEnabledEnvironmentVariable is a constant from internal/civisibility/constants.
	// We require the walker to resolve at least one such cross-package constant.
	if _, ok := got["DD_CIVISIBILITY_ENABLED"]; !ok {
		t.Errorf("expected DD_CIVISIBILITY_ENABLED (resolved from constant) in migrated set")
	}
}

func TestLoadMigrated_RejectsDuplicateRawKeys(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), `duplicate raw key "DD_SERVICE"`) {
		t.Fatalf("got error %v, want duplicate raw key", err)
	}
}

func TestLoadMigrated_RejectsDuplicateBindingIDs(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), `duplicate binding ID "tracer.service"`) {
		t.Fatalf("got error %v, want duplicate binding ID", err)
	}
}

func TestLoadMigrated_RejectsBindingWithMissingRawKey(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), `binding "tracer.service" references unregistered raw key "DD_SERVICE"`) {
		t.Fatalf("got error %v, want missing raw key", err)
	}
}

func TestLoadMigrated_RejectsRawWithoutBinding(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), `raw key "DD_SERVICE" has no consumer binding`) {
		t.Fatalf("got error %v, want raw key without binding", err)
	}
}

func TestLoadMigrated_RejectsNonconstantDeclarations(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: nonconstantString(), Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), "raw definition key must be constant") {
		t.Fatalf("got error %v, want nonconstant key", err)
	}
}

func TestLoadMigrated_RejectsNonconstantBindingID(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: nonconstantString(), Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), "consumer binding ID must be constant") {
		t.Fatalf("got error %v, want nonconstant binding ID", err)
	}
}

func TestLoadMigrated_RejectsNonconstantBindingKey(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{nonconstantString()}, Sampling: SampleTracerConstruction})
`)
	_, err := loadMigrated(pkgDir)
	if err == nil || !strings.Contains(err.Error(), "consumer binding key must be constant") {
		t.Fatalf("got error %v, want nonconstant binding key", err)
	}
}

func TestLoadMigrated_IgnoresShadowedRegistrationHelpers(t *testing.T) {
	pkgDir := writeRegistryFixture(t, `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
	func() {
		registerRaw := func(RawDefinition) {}
		key := "DD_NOT_REGISTERED"
		registerRaw(RawDefinition{Key: key, Sources: SourceStable, Telemetry: TelemetryReport})
	}()
`)
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got migrated keys %#v, want only DD_SERVICE", got)
	}
}

func TestLoadMigrated_RejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name         string
		declarations string
		wantErr      string
	}{
		{
			name: "missing raw key",
			declarations: `
	registerRaw(RawDefinition{Sources: SourceStable, Telemetry: TelemetryReport})
`,
			wantErr: "raw definition key must be constant",
		},
		{
			name: "empty raw key",
			declarations: `
	registerRaw(RawDefinition{Key: "", Sources: SourceStable, Telemetry: TelemetryReport})
`,
			wantErr: "raw definition key must not be empty",
		},
		{
			name: "missing source",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Telemetry: TelemetryReport})
`,
			wantErr: "raw definition source policy must be constant",
		},
		{
			name: "nonconstant source",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: nonconstantSource(), Telemetry: TelemetryReport})
`,
			wantErr: "raw definition source policy must be constant",
		},
		{
			name: "out of range source",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourcePolicy(2), Telemetry: TelemetryReport})
`,
			wantErr: `raw key "DD_SERVICE" has invalid source policy 2`,
		},
		{
			name: "missing telemetry",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
`,
			wantErr: "raw definition telemetry policy must be constant",
		},
		{
			name: "nonconstant telemetry",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: nonconstantTelemetry()})
`,
			wantErr: "raw definition telemetry policy must be constant",
		},
		{
			name: "out of range telemetry",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryPolicy(4)})
`,
			wantErr: `raw key "DD_SERVICE" has invalid telemetry policy 4`,
		},
		{
			name: "missing binding ID",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`,
			wantErr: "consumer binding ID must be constant",
		},
		{
			name: "empty binding ID",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`,
			wantErr: "consumer binding ID must not be empty",
		},
		{
			name: "missing consumer",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`,
			wantErr: "consumer binding consumer must be constant",
		},
		{
			name: "empty consumer",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`,
			wantErr: `binding "tracer.service" has an empty consumer`,
		},
		{
			name: "nonconstant consumer",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: nonconstantString(), Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})
`,
			wantErr: "consumer binding consumer must be constant",
		},
		{
			name: "missing keys",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Sampling: SampleTracerConstruction})
`,
			wantErr: `consumer binding "tracer.service" has no raw keys`,
		},
		{
			name: "empty keys",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{}, Sampling: SampleTracerConstruction})
`,
			wantErr: `binding "tracer.service" has no raw keys`,
		},
		{
			name: "missing sampling",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}})
`,
			wantErr: "consumer binding sampling boundary must be constant",
		},
		{
			name: "nonconstant sampling",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: nonconstantSampling()})
`,
			wantErr: "consumer binding sampling boundary must be constant",
		},
		{
			name: "out of range sampling",
			declarations: `
	registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SamplingBoundary(6)})
`,
			wantErr: `binding "tracer.service" has invalid sampling boundary 6`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pkgDir := writeRegistryFixture(t, test.declarations)
			_, err := loadMigrated(pkgDir)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("got error %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadMigrated_RejectsRegistrationsNotGuaranteedAtInit(t *testing.T) {
	validRaw := `registerRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})`
	validBinding := `registerBinding(ConsumerBinding{ID: "tracer.service", Consumer: "tracer", Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction})`
	tests := []struct {
		name      string
		functions string
	}{
		{
			name: "unused function",
			functions: fmt.Sprintf(`
func unused() {
	%s
	%s
}
func init() {}
`, validRaw, validBinding),
		},
		{
			name: "conditional init",
			functions: fmt.Sprintf(`
func init() {
	if true {
		%s
		%s
	}
}
`, validRaw, validBinding),
		},
		{
			name: "loop in init",
			functions: fmt.Sprintf(`
func init() {
	for range 1 {
		%s
		%s
	}
}
`, validRaw, validBinding),
		},
		{
			name: "switch in init",
			functions: fmt.Sprintf(`
func init() {
	switch {
	case true:
		%s
		%s
	}
}
`, validRaw, validBinding),
		},
		{
			name: "closure in init",
			functions: fmt.Sprintf(`
func init() {
	func() {
		%s
		%s
	}()
}
`, validRaw, validBinding),
		},
		{
			name: "unreachable init",
			functions: fmt.Sprintf(`
func init() {
	return
	%s
	%s
}
`, validRaw, validBinding),
		},
		{
			name: "after infinite loop",
			functions: fmt.Sprintf(`
func init() {
	for {}
	%s
	%s
}
`, validRaw, validBinding),
		},
		{
			name: "after panic",
			functions: fmt.Sprintf(`
func init() {
	panic("stop")
	%s
	%s
}
`, validRaw, validBinding),
		},
		{
			name: "after conditional return",
			functions: fmt.Sprintf(`
var stop bool

func init() {
	if stop {
		return
	}
	%s
	%s
}
`, validRaw, validBinding),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pkgDir := writeRegistryFunctionsFixture(t, test.functions)
			_, err := loadMigrated(pkgDir)
			if err == nil || !strings.Contains(err.Error(), "registration must be a direct statement in the registration prefix of func init") {
				t.Fatalf("got error %v, want non-init registration error", err)
			}
		})
	}
}

func writeRegistryFixture(t *testing.T, declarations string) string {
	t.Helper()
	return writeRegistryFunctionsFixture(t, fmt.Sprintf(`
func init() {
%s
}
`, declarations))
}

func writeRegistryFunctionsFixture(t *testing.T, functions string) string {
	t.Helper()
	dir := t.TempDir()
	source := fmt.Sprintf(`package config

type SourcePolicy uint8
const (
	SourceEnvironment SourcePolicy = iota
	SourceStable
)
type TelemetryPolicy uint8
const (
	TelemetryReport TelemetryPolicy = iota
	TelemetryRedact
	TelemetrySanitizeURL
	TelemetryOmit
)
type SamplingBoundary uint8
const (
	SamplePackageInit SamplingBoundary = iota
	SampleTracerConstruction
	SampleProductStart
	SampleConstructor
	SampleFirstUse
	SamplePerCall
)
type RawDefinition struct {
	Key string
	Sources SourcePolicy
	Telemetry TelemetryPolicy
}
type ConsumerBinding struct {
	ID string
	Consumer string
	Keys []string
	Sampling SamplingBoundary
}

func registerRaw(RawDefinition) {}
func registerBinding(ConsumerBinding) {}
func nonconstantString() string { return "dynamic" }
func nonconstantSource() SourcePolicy { return SourceStable }
func nonconstantTelemetry() TelemetryPolicy { return TelemetryReport }
func nonconstantSampling() SamplingBoundary { return SampleTracerConstruction }

%s
`, functions)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/config\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "definitions.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
