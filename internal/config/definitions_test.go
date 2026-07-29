// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryAllowsConsumerSpecificBindings(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	r.addBinding(ConsumerBinding{
		ID: "profiler.service", Consumer: "profiler",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleProductStart,
	})
	require.NoError(t, r.validate())
}

func TestRegistryAllowsConsumerSourceNarrowing(t *testing.T) {
	t.Run("stable raw can be narrowed to environment", func(t *testing.T) {
		r := newRegistry()
		r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
		r.addBinding(ConsumerBinding{
			ID: "tracer.service", Consumer: "tracer",
			Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
		})
		r.addBinding(ConsumerBinding{
			ID: "naming.service", Consumer: "naming",
			Keys: []string{"DD_SERVICE"}, Sampling: SamplePackageInit,
			EnvironmentOnly: true,
		})
		require.NoError(t, r.validate())
	})

	t.Run("environment raw tolerates redundant narrowing", func(t *testing.T) {
		r := newRegistry()
		r.addRaw(RawDefinition{Key: "DD_VALUE", Sources: SourceEnvironment})
		r.addBinding(ConsumerBinding{
			ID: "consumer.value", Consumer: "consumer",
			Keys: []string{"DD_VALUE"}, Sampling: SampleConstructor,
			EnvironmentOnly: true,
		})
		require.NoError(t, r.validate())
	})
}

func TestRegistryRejectsDuplicateRawKeys(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceEnvironment})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	require.ErrorContains(t, r.validate(), `duplicate raw key "DD_SERVICE"`)
}

func TestRegistryRejectsDuplicateBindingIDs(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "profiler",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleProductStart,
	})
	require.ErrorContains(t, r.validate(), `duplicate binding ID "tracer.service"`)
}

func TestRegistryRejectsBindingWithMissingRawKey(t *testing.T) {
	r := newRegistry()
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	require.ErrorContains(t, r.validate(), `binding "tracer.service" references unregistered raw key "DD_SERVICE"`)
}

func TestRegistryRejectsRawKeyWithoutBinding(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	require.ErrorContains(t, r.validate(), `raw key "DD_SERVICE" has no consumer binding`)
}

func TestRegistryDefinitionsAreSortedDefensiveCopies(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_VERSION", Sources: SourceStable})
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	r.addBinding(ConsumerBinding{
		ID: "tracer.version", Consumer: "tracer",
		Keys: []string{"DD_VERSION"}, Sampling: SampleTracerConstruction,
	})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	require.NoError(t, r.validate())

	raw, bindings := r.definitions()
	require.Equal(t, []string{"DD_SERVICE", "DD_VERSION"}, []string{raw[0].Key, raw[1].Key})
	require.Equal(t, []string{"tracer.service", "tracer.version"}, []string{bindings[0].ID, bindings[1].ID})

	raw[0].Key = "changed"
	bindings[0].Keys[0] = "changed"
	raw, bindings = r.definitions()
	require.Equal(t, "DD_SERVICE", raw[0].Key)
	require.Equal(t, []string{"DD_SERVICE"}, bindings[0].Keys)
}

func TestRegistryRegisteredDefinitionsValidate(t *testing.T) {
	raw, bindings := RegisteredDefinitions()
	require.Len(t, raw, 252)
	require.Len(t, bindings, 178)
}

func TestRegistryFirstReadValidatesCompleteRegistryAndFreezesOnFailure(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})

	require.PanicsWithValue(t, `config registry: duplicate raw key "DD_SERVICE"`, func() {
		_, _ = r.rawDefinition("DD_SERVICE")
	})
	rawCount := len(r.raw)
	require.PanicsWithValue(t, "config registry is frozen", func() {
		r.addRaw(RawDefinition{Key: "DD_VERSION"})
	})
	require.Len(t, r.raw, rawCount, "registration must panic before mutating a frozen registry")
}

func TestRegistryEveryFirstReadValidatesFinalRegistry(t *testing.T) {
	tests := []struct {
		name string
		read func(*registry)
	}{
		{name: "definitions", read: func(r *registry) { _, _ = r.definitions() }},
		{name: "raw definition", read: func(r *registry) { _, _ = r.rawDefinition("DD_SERVICE") }},
		{name: "reporter metadata", read: func(r *registry) {
			_, _, _ = r.reporterMetadata("tracer.service", "DD_SERVICE")
		}},
		{name: "reporter binding", read: func(r *registry) {
			_, _ = r.reporterBinding(ConsumerBinding{
				ID: "tracer.service", Consumer: "tracer",
				Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := newRegistry()
			r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
			r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
			r.addBinding(ConsumerBinding{
				ID: "tracer.service", Consumer: "tracer",
				Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
			})

			for range 2 {
				require.PanicsWithValue(t, `config registry: duplicate raw key "DD_SERVICE"`, func() {
					test.read(r)
				}, "a failed final validation must remain sticky")
			}
		})
	}
}

func TestRegistryPrefixValidationDoesNotFreezeFinalRegistrations(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	require.NoError(t, r.validate(), "the definitions.go prefix check remains non-freezing")

	r.addRaw(RawDefinition{Key: "DD_VERSION", Sources: SourceStable, Telemetry: TelemetryReport})
	r.addBinding(ConsumerBinding{
		ID: "tracer.version", Consumer: "tracer",
		Keys: []string{"DD_VERSION"}, Sampling: SampleTracerConstruction,
	})
	definition, ok := r.rawDefinition("DD_VERSION")
	require.True(t, ok)
	require.Equal(t, "DD_VERSION", definition.Key)

	rawCount, bindingCount := len(r.raw), len(r.bindings)
	require.PanicsWithValue(t, "config registry is frozen", func() {
		r.addBinding(ConsumerBinding{ID: "late.binding"})
	})
	require.Len(t, r.raw, rawCount)
	require.Len(t, r.bindings, bindingCount, "registration must panic before mutating a frozen registry")
}

func TestRegistryRejectsInvalidDefinitions(t *testing.T) {
	validRaw := RawDefinition{Key: "DD_SERVICE", Sources: SourceStable, Telemetry: TelemetryReport}
	validBinding := ConsumerBinding{
		ID:       "tracer.service",
		Consumer: "tracer",
		Keys:     []string{"DD_SERVICE"},
		Sampling: SampleTracerConstruction,
	}
	tests := []struct {
		name    string
		build   func(*registry)
		wantErr string
	}{
		{
			name: "empty raw key",
			build: func(r *registry) {
				r.addRaw(RawDefinition{Sources: SourceStable, Telemetry: TelemetryReport})
			},
			wantErr: "raw definition has an empty key",
		},
		{
			name: "empty binding ID",
			build: func(r *registry) {
				r.addRaw(validRaw)
				binding := validBinding
				binding.ID = ""
				r.addBinding(binding)
			},
			wantErr: "consumer binding has an empty ID",
		},
		{
			name: "empty consumer",
			build: func(r *registry) {
				r.addRaw(validRaw)
				binding := validBinding
				binding.Consumer = ""
				r.addBinding(binding)
			},
			wantErr: `binding "tracer.service" has an empty consumer`,
		},
		{
			name: "empty keys",
			build: func(r *registry) {
				r.addRaw(validRaw)
				binding := validBinding
				binding.Keys = nil
				r.addBinding(binding)
			},
			wantErr: `binding "tracer.service" has no raw keys`,
		},
		{
			name: "invalid source",
			build: func(r *registry) {
				raw := validRaw
				raw.Sources = SourcePolicy(2)
				r.addRaw(raw)
			},
			wantErr: `raw key "DD_SERVICE" has invalid source policy 2`,
		},
		{
			name: "invalid telemetry",
			build: func(r *registry) {
				raw := validRaw
				raw.Telemetry = TelemetryPolicy(4)
				r.addRaw(raw)
			},
			wantErr: `raw key "DD_SERVICE" has invalid telemetry policy 4`,
		},
		{
			name: "invalid sampling",
			build: func(r *registry) {
				r.addRaw(validRaw)
				binding := validBinding
				binding.Sampling = SamplingBoundary(6)
				r.addBinding(binding)
			},
			wantErr: `binding "tracer.service" has invalid sampling boundary 6`,
		},
		{
			name: "duplicate raw",
			build: func(r *registry) {
				r.addRaw(validRaw)
				r.addRaw(validRaw)
			},
			wantErr: `duplicate raw key "DD_SERVICE"`,
		},
		{
			name: "duplicate binding",
			build: func(r *registry) {
				r.addRaw(validRaw)
				r.addBinding(validBinding)
				r.addBinding(validBinding)
			},
			wantErr: `duplicate binding ID "tracer.service"`,
		},
		{
			name: "missing raw",
			build: func(r *registry) {
				r.addBinding(validBinding)
			},
			wantErr: `binding "tracer.service" references unregistered raw key "DD_SERVICE"`,
		},
		{
			name: "raw without binding",
			build: func(r *registry) {
				r.addRaw(validRaw)
			},
			wantErr: `raw key "DD_SERVICE" has no consumer binding`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := newRegistry()
			test.build(r)
			require.ErrorContains(t, r.validate(), test.wantErr)
		})
	}
}
