package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestOtelEnvConfigSource(t *testing.T) {
	t.Run("maps OTEL_SERVICE_NAME to service", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.Get("service")
		assert.Equal(t, "my-service", v)
	})

	t.Run("maps OTEL_SERVICE_NAME with DD_SERVICE key", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_SERVICE")
		assert.Equal(t, "my-service", v)
	})

	t.Run("returns empty when only DD var is set", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "my-service")
		source := &otelEnvConfigSource{}
		v := source.Get("service")
		assert.Equal(t, "", v, "otelEnvConfigSource should not read DD vars directly")
	})

	t.Run("maps OTEL_TRACES_SAMPLER to sample rate", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_always_on")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_SAMPLE_RATE")
		assert.Equal(t, "1.0", v)
	})

	t.Run("maps OTEL_TRACES_SAMPLER with sampler arg", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_SAMPLE_RATE")
		assert.Equal(t, "0.5", v)
	})

	t.Run("maps OTEL_LOG_LEVEL=debug to DD_TRACE_DEBUG=true", func(t *testing.T) {
		t.Setenv("OTEL_LOG_LEVEL", "debug")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_DEBUG")
		assert.Equal(t, "true", v)
	})

	t.Run("returns empty for invalid OTEL_LOG_LEVEL", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_LOG_LEVEL", "invalid")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_DEBUG")

		assert.Equal(t, "", v)
		assert.NotZero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{"config_datadog:dd_trace_debug", "config_opentelemetry:otel_log_level"}).Get())
	})

	t.Run("maps OTEL_TRACES_EXPORTER=none to DD_TRACE_ENABLED=false", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_ENABLED")
		assert.Equal(t, "false", v)
	})

	t.Run("returns empty for invalid OTEL_TRACES_EXPORTER", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("OTEL_TRACES_EXPORTER", "jaeger")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_ENABLED")

		assert.Equal(t, "", v)
		assert.NotZero(t, telemetryClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", []string{"config_datadog:dd_trace_enabled", "config_opentelemetry:otel_traces_exporter"}).Get())
	})

	t.Run("maps OTEL_METRICS_EXPORTER=none to DD_RUNTIME_METRICS_ENABLED=false", func(t *testing.T) {
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_RUNTIME_METRICS_ENABLED")
		assert.Equal(t, "false", v)
	})

	t.Run("maps OTEL_PROPAGATORS to DD_TRACE_PROPAGATION_STYLE", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "tracecontext,b3")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TRACE_PROPAGATION_STYLE")
		assert.Equal(t, "tracecontext,b3 single header", v)
	})

	t.Run("maps OTEL_RESOURCE_ATTRIBUTES to DD_TAGS", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=my-service,deployment.environment=prod,custom.key=value")
		source := &otelEnvConfigSource{}
		v := source.Get("DD_TAGS")

		// service.name should be mapped to "service"
		assert.Contains(t, v, "service:my-service")
		assert.Contains(t, v, "env:prod")
		assert.Contains(t, v, "custom.key:value")
	})

	t.Run("returns empty for unsupported key", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		source := &otelEnvConfigSource{}
		v := source.Get("UNSUPPORTED_KEY")
		assert.Equal(t, "", v)
	})

	t.Run("returns empty when OTEL var not set", func(t *testing.T) {
		source := &otelEnvConfigSource{}
		v := source.Get("DD_SERVICE")
		assert.Equal(t, "", v)
	})

	t.Run("origin returns OriginEnvVar", func(t *testing.T) {
		source := &otelEnvConfigSource{}
		assert.Equal(t, telemetry.OriginEnvVar, source.Origin())
	})
}
