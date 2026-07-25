// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestOtelEnvConfigSourceSamplerArgumentLookup(t *testing.T) {
	const key = "OTEL_TRACES_SAMPLER_ARG"
	old, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			require.NoError(t, os.Setenv(key, old))
		} else {
			require.NoError(t, os.Unsetenv(key))
		}
	})

	source := new(otelEnvConfigSource)
	t.Run("absent", func(t *testing.T) {
		require.Equal(t, "1.0", source.lookupSamplerArgument())
	})
	t.Run("explicit empty", func(t *testing.T) {
		t.Setenv(key, "")
		require.Equal(t, "1.0", source.lookupSamplerArgument())
	})
	t.Run("resamples changes", func(t *testing.T) {
		t.Setenv(key, "0.25")
		require.Equal(t, "0.25", source.lookupSamplerArgument())
		t.Setenv(key, "0.75")
		require.Equal(t, "0.75", source.lookupSamplerArgument())
	})
}

func TestOtelEnvConfigSource(t *testing.T) {
	t.Run("maps OTEL_SERVICE_NAME to service", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.get("service")
		assert.Equal(t, "my-service", v)
	})

	t.Run("maps OTEL_SERVICE_NAME with DD_SERVICE key", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.get("DD_SERVICE")
		assert.Equal(t, "my-service", v)
	})

	t.Run("returns empty when only DD var is set", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "my-service")
		source := &otelEnvConfigSource{}
		v := source.get("service")
		assert.Equal(t, "", v, "otelEnvConfigSource should not read DD vars directly")
	})

	t.Run("maps OTEL_TRACES_SAMPLER to sample rate", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_always_on")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TRACE_SAMPLE_RATE")
		assert.Equal(t, "1.0", v)
	})

	t.Run("maps OTEL_TRACES_SAMPLER with sampler arg", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TRACE_SAMPLE_RATE")
		assert.Equal(t, "0.5", v)
	})

	t.Run("maps OTEL_LOG_LEVEL=debug to DD_TRACE_DEBUG=true", func(t *testing.T) {
		t.Setenv("OTEL_LOG_LEVEL", "debug")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TRACE_DEBUG")
		assert.Equal(t, "true", v)
	})

	t.Run("returns empty for invalid OTEL_LOG_LEVEL", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_LOG_LEVEL", "invalid")
		p := newTestProvider(&otelEnvConfigSource{})
		v := p.GetBool("DD_TRACE_DEBUG", false)

		assert.False(t, v)
		assert.NotZero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{"config_datadog:dd_trace_debug", "config_opentelemetry:otel_log_level"}).Get())
	})

	t.Run("maps OTEL_TRACES_EXPORTER=none to DD_TRACE_ENABLED=false", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TRACE_ENABLED")
		assert.Equal(t, "false", v)
	})

	t.Run("returns empty for invalid OTEL_TRACES_EXPORTER", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_TRACES_EXPORTER", "jaeger")
		p := newTestProvider(&otelEnvConfigSource{})
		v := p.GetBool("DD_TRACE_ENABLED", true)

		assert.True(t, v)
		assert.NotZero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{"config_datadog:dd_trace_enabled", "config_opentelemetry:otel_traces_exporter"}).Get())
	})

	t.Run("compatibility getter preserves hiding metric", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_SERVICE_NAME", "otel-service")
		t.Setenv("DD_SERVICE", "dd-service")
		p := newTestProvider(new(envConfigSource), new(otelEnvConfigSource))

		assert.Equal(t, "dd-service", p.GetString("DD_SERVICE", "default"))
		assert.NotZero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.hiding", []string{"config_datadog:dd_service", "config_opentelemetry:otel_service_name"}).Get())
	})

	t.Run("explicit empty DD is a generic hiding diagnostic only", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_SERVICE_NAME", "otel-service")
		t.Setenv("DD_SERVICE", "")
		p := newTestProvider(new(envConfigSource), new(otelEnvConfigSource))

		got := Resolve(p, testDefinition("DD_SERVICE", schema.SourceStable), "default", parseTestString)
		require.Equal(t, "", got.Winner.Value)
		require.Equal(t, telemetry.OriginEnvVar, got.Winner.Origin)
		require.Equal(t, EventOTelEnvHiding, findEvent(t, got.Events, EventOTelEnvHiding).Kind)
		require.Zero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.hiding", []string{"config_datadog:dd_service", "config_opentelemetry:otel_service_name"}).Get())

		require.Equal(t, "otel-service", p.GetString("DD_SERVICE", "default"))
		require.Zero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.hiding", []string{"config_datadog:dd_service", "config_opentelemetry:otel_service_name"}).Get())
	})

	t.Run("explicit empty OTel is also a generic hiding diagnostic only", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_SERVICE_NAME", "")
		t.Setenv("DD_SERVICE", "dd-service")
		p := newTestProvider(new(envConfigSource), new(otelEnvConfigSource))

		got := Resolve(p, testDefinition("DD_SERVICE", schema.SourceStable), "default", parseTestString)
		require.Equal(t, "dd-service", got.Winner.Value)
		require.Equal(t, EventOTelEnvHiding, findEvent(t, got.Events, EventOTelEnvHiding).Kind)

		require.Equal(t, "dd-service", p.GetString("DD_SERVICE", "default"))
		require.Zero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.hiding", []string{"config_datadog:dd_service", "config_opentelemetry:otel_service_name"}).Get())
	})

	t.Run("maps OTEL_METRICS_EXPORTER=none to DD_RUNTIME_METRICS_ENABLED=false", func(t *testing.T) {
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		source := &otelEnvConfigSource{}
		v := source.get("DD_RUNTIME_METRICS_ENABLED")
		assert.Equal(t, "false", v)
	})

	t.Run("OTEL_METRICS_EXPORTER=otlp does not flag DD_RUNTIME_METRICS_ENABLED as unsupported", func(t *testing.T) {
		// otlp is a valid exporter; it must not produce a "not supported" warning/telemetry
		// for the DD_RUNTIME_METRICS_ENABLED mapping, nor set a value.
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		source := &otelEnvConfigSource{}
		v := source.get("DD_RUNTIME_METRICS_ENABLED")
		assert.Equal(t, "", v)
		assert.Zero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{"config_datadog:dd_runtime_metrics_enabled", "config_opentelemetry:otel_metrics_exporter"}).Get())
	})

	t.Run("maps OTEL_METRICS_EXPORTER=none to DD_METRICS_OTEL_ENABLED=false", func(t *testing.T) {
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		source := &otelEnvConfigSource{}
		v := source.get("DD_METRICS_OTEL_ENABLED")
		assert.Equal(t, "false", v)
	})

	t.Run("OTEL_METRICS_EXPORTER=otlp does not enable DD_METRICS_OTEL_ENABLED (opt-in)", func(t *testing.T) {
		// OTel runtime metrics are opt-in: only DD_METRICS_OTEL_ENABLED=true enables them.
		// The exporter being otlp must not flip enablement on its own.
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		source := &otelEnvConfigSource{}
		v := source.get("DD_METRICS_OTEL_ENABLED")
		assert.Equal(t, "", v)
	})

	t.Run("successful empty remaps remain non-applicable attempts", func(t *testing.T) {
		tests := []struct {
			name    string
			key     string
			otelKey string
			otel    string
			local   string
			def     any
		}{
			{
				name: "unsupported propagators", key: "DD_TRACE_PROPAGATION_STYLE",
				otelKey: "OTEL_PROPAGATORS", otel: "unsupported", local: "datadog", def: "default",
			},
			{
				name: "runtime metrics exporter", key: "DD_RUNTIME_METRICS_ENABLED",
				otelKey: "OTEL_METRICS_EXPORTER", otel: "otlp", local: "true", def: false,
			},
			{
				name: "otel metrics exporter", key: "DD_METRICS_OTEL_ENABLED",
				otelKey: "OTEL_METRICS_EXPORTER", otel: "otlp", local: "true", def: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				telemetryClient := new(telemetrytest.RecordClient)
				defer telemetry.MockClient(telemetryClient)()
				t.Setenv(tt.otelKey, tt.otel)
				local := &resolveTestSource{
					raw: tt.local, present: true, originValue: telemetry.OriginLocalStableConfig,
				}
				p := newTestProvider(new(otelEnvConfigSource), local)

				switch def := tt.def.(type) {
				case string:
					got := Resolve(p, testDefinition(tt.key, schema.SourceStable), def, parseTestString)
					require.Equal(t, tt.local, got.Winner.Value)
					require.Equal(t, tt.local, p.GetString(tt.key, def))
					require.Len(t, got.Attempts, 2)
					require.True(t, got.Attempts[1].Present)
					require.False(t, got.Attempts[1].Valid)
					require.NoError(t, got.Attempts[1].Err)
				case bool:
					got := Resolve(p, testDefinition(tt.key, schema.SourceStable), def, strconv.ParseBool)
					require.True(t, got.Winner.Value)
					require.True(t, p.GetBool(tt.key, def))
					require.Len(t, got.Attempts, 2)
					require.True(t, got.Attempts[1].Present)
					require.False(t, got.Attempts[1].Valid)
					require.NoError(t, got.Attempts[1].Err)
				}
				require.Zero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{
					ddPrefix + strings.ToLower(tt.key),
					otelPrefix + strings.ToLower(tt.otelKey),
				}).Get())
			})
		}
	})

	t.Run("pass-through explicit empty remains applicable", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "")
		p := newTestProvider(new(otelEnvConfigSource))

		got := Resolve(p, testDefinition("DD_SERVICE", schema.SourceStable), "default", parseTestString)

		require.Equal(t, "", got.Winner.Value)
		require.Equal(t, telemetry.OriginEnvVar, got.Winner.Origin)
		require.Len(t, got.Attempts, 1)
		require.True(t, got.Attempts[0].Present)
		require.True(t, got.Attempts[0].Valid)
		require.NoError(t, got.Attempts[0].Err)
	})

	t.Run("maps OTEL_PROPAGATORS to DD_TRACE_PROPAGATION_STYLE", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "tracecontext,b3")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TRACE_PROPAGATION_STYLE")
		assert.Equal(t, "tracecontext,b3 single header", v)
	})

	t.Run("maps OTEL_RESOURCE_ATTRIBUTES to DD_TAGS", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=my-service,deployment.environment=prod,custom.key=value")
		source := &otelEnvConfigSource{}
		v := source.get("DD_TAGS")

		assert.Contains(t, v, "service:my-service")
		assert.Contains(t, v, "env:prod")
		assert.Contains(t, v, "custom.key:value")
	})

	t.Run("returns empty for unsupported key", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.get("UNSUPPORTED_KEY")
		assert.Equal(t, "", v)
	})

	t.Run("returns empty when OTEL var not set", func(t *testing.T) {
		source := &otelEnvConfigSource{}
		v := source.get("DD_SERVICE")
		assert.Equal(t, "", v)
	})

	t.Run("origin returns OriginEnvVar", func(t *testing.T) {
		source := &otelEnvConfigSource{}
		assert.Equal(t, telemetry.OriginEnvVar, source.origin())
	})
}

func TestOtelEnvConfigSourceNonemptyDDShortCircuitsLoserRemapping(t *testing.T) {
	tests := []struct {
		name       string
		otel       string
		wantEvents int
		wantLog    string
	}{
		{name: "explicit empty OTel", wantEvents: 1},
		{
			name:       "bogus OTel",
			otel:       "not-a-propagator",
			wantEvents: 1,
			wantLog:    `Both "OTEL_PROPAGATORS" and "DD_TRACE_PROPAGATION_STYLE" are set, using DD_TRACE_PROPAGATION_STYLE=datadog`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()
			t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog")
			t.Setenv("OTEL_PROPAGATORS", tt.otel)

			raw, present, applicable, err, events := new(otelEnvConfigSource).lookupWithEvents("DD_TRACE_PROPAGATION_STYLE")

			require.Equal(t, tt.otel, raw)
			require.True(t, present)
			require.False(t, applicable)
			require.NoError(t, err)
			require.Len(t, events, tt.wantEvents)
			if tt.wantLog == "" {
				require.Empty(t, logger.Logs())
				require.Equal(t, EventOTelEnvHiding, events[0].Kind)
				require.False(t, events[0].CompatibilityReport)
			} else {
				require.Len(t, logger.Logs(), 1)
				require.Contains(t, logger.Logs()[0], tt.wantLog)
				require.Equal(t, EventOTelEnvHiding, events[0].Kind)
			}
		})
	}
}

func TestOtelEnvConfigSourceExplicitEmptySkipsRemapping(t *testing.T) {
	tests := []struct {
		ddKey   string
		otelKey string
	}{
		{ddKey: "DD_RUNTIME_METRICS_ENABLED", otelKey: "OTEL_METRICS_EXPORTER"},
		{ddKey: "DD_TRACE_DEBUG", otelKey: "OTEL_LOG_LEVEL"},
		{ddKey: "DD_TRACE_ENABLED", otelKey: "OTEL_TRACES_EXPORTER"},
		{ddKey: "DD_TRACE_SAMPLE_RATE", otelKey: "OTEL_TRACES_SAMPLER"},
		{ddKey: "DD_TRACE_PROPAGATION_STYLE", otelKey: "OTEL_PROPAGATORS"},
		{ddKey: "DD_TAGS", otelKey: "OTEL_RESOURCE_ATTRIBUTES"},
	}
	for _, tt := range tests {
		t.Run(tt.otelKey, func(t *testing.T) {
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()
			t.Setenv(tt.otelKey, "")

			raw, present, applicable, err, events := new(otelEnvConfigSource).lookupWithEvents(tt.ddKey)

			require.Empty(t, raw)
			require.True(t, present)
			require.False(t, applicable)
			require.NoError(t, err)
			require.Empty(t, events)
			require.Empty(t, logger.Logs())
		})
	}

	t.Run("OTEL_SERVICE_NAME remains applicable", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "")
		raw, present, applicable, err, events := new(otelEnvConfigSource).lookupWithEvents("DD_SERVICE")
		require.Empty(t, raw)
		require.True(t, present)
		require.True(t, applicable)
		require.NoError(t, err)
		require.Empty(t, events)
	})
}
