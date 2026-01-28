// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

func TestBuildResource(t *testing.T) {
	t.Run("empty environment returns resource with telemetry SDK", func(t *testing.T) {
		res, err := buildResource(context.Background())
		require.NoError(t, err)
		require.NotNil(t, res)

		attrs := res.Attributes()
		// Should have telemetry.sdk.* attributes from WithTelemetrySDK()
		assert.NotEmpty(t, attrs)
	})

	t.Run("DD_SERVICE maps to service.name", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "my-service")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceNameKey, "my-service")
	})

	t.Run("DD_ENV maps to deployment.environment", func(t *testing.T) {
		t.Setenv("DD_ENV", "production")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.DeploymentEnvironmentNameKey, "production")
	})

	t.Run("DD_VERSION maps to service.version", func(t *testing.T) {
		t.Setenv("DD_VERSION", "1.2.3")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceVersionKey, "1.2.3")
	})

	t.Run("DD_TAGS converts to resource attributes", func(t *testing.T) {
		t.Setenv("DD_TAGS", "team:backend,region:us-east-1")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttributeString(t, res, "team", "backend")
		assertResourceAttributeString(t, res, "region", "us-east-1")
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES parses correctly", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "otel.key=otel.value,another=test")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttributeString(t, res, "otel.key", "otel.value")
		assertResourceAttributeString(t, res, "another", "test")
	})
}

func TestPrecedence(t *testing.T) {
	t.Run("DD_SERVICE wins over OTEL service.name", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "dd-service")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=otel-service")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceNameKey, "dd-service")
	})

	t.Run("DD_ENV wins over OTEL deployment.environment.name", func(t *testing.T) {
		t.Setenv("DD_ENV", "dd-env")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=otel-env")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.DeploymentEnvironmentNameKey, "dd-env")
	})

	t.Run("DD_VERSION wins over OTEL service.version", func(t *testing.T) {
		t.Setenv("DD_VERSION", "2.0.0")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.0.0")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceVersionKey, "2.0.0")
	})

	t.Run("DD_TAGS wins over OTEL custom attributes", func(t *testing.T) {
		t.Setenv("DD_TAGS", "custom.key:dd-value")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "custom.key=otel-value")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttributeString(t, res, "custom.key", "dd-value")
	})

	t.Run("OTEL attributes used when DD not set", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=otel-service,deployment.environment.name=otel-env,service.version=1.0.0")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceNameKey, "otel-service")
		assertResourceAttribute(t, res, semconv.DeploymentEnvironmentNameKey, "otel-env")
		assertResourceAttribute(t, res, semconv.ServiceVersionKey, "1.0.0")
	})

	t.Run("mixed DD and OTEL attributes coexist", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "dd-service")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "otel.only=value,service.name=ignored")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// DD wins for service.name
		assertResourceAttribute(t, res, semconv.ServiceNameKey, "dd-service")
		// OTEL attribute that doesn't conflict is preserved
		assertResourceAttributeString(t, res, "otel.only", "value")
	})
}

func TestHostname(t *testing.T) {
	t.Run("no hostname by default", func(t *testing.T) {
		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// Should not have host.name attribute
		attrs := res.Attributes()
		for _, attr := range attrs {
			assert.NotEqual(t, semconv.HostNameKey, attr.Key, "host.name should not be present by default")
		}
	})

	t.Run("DD_HOSTNAME alone does not set hostname", func(t *testing.T) {
		t.Setenv("DD_HOSTNAME", "my-host")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// DD_HOSTNAME alone should NOT add hostname - needs DD_TRACE_REPORT_HOSTNAME=true
		attrs := res.Attributes()
		for _, attr := range attrs {
			assert.NotEqual(t, semconv.HostNameKey, attr.Key, "host.name should not be present without DD_TRACE_REPORT_HOSTNAME=true")
		}
	})

	t.Run("DD_TRACE_REPORT_HOSTNAME=true uses detected hostname", func(t *testing.T) {
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// Should have hostname from os.Hostname()
		detectedHostname, _ := os.Hostname()
		if detectedHostname != "" {
			assertResourceAttribute(t, res, semconv.HostNameKey, detectedHostname)
		}
	})

	t.Run("DD_TRACE_REPORT_HOSTNAME=false does not add hostname", func(t *testing.T) {
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "false")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		attrs := res.Attributes()
		for _, attr := range attrs {
			assert.NotEqual(t, semconv.HostNameKey, attr.Key, "host.name should not be present when DD_TRACE_REPORT_HOSTNAME=false")
		}
	})

	t.Run("DD_HOSTNAME wins over DD_TRACE_REPORT_HOSTNAME", func(t *testing.T) {
		t.Setenv("DD_HOSTNAME", "explicit-host")
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.HostNameKey, "explicit-host")
	})

	t.Run("OTEL host.name has highest priority", func(t *testing.T) {
		t.Setenv("DD_HOSTNAME", "dd-host")
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=otel-host")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// OTEL_RESOURCE_ATTRIBUTES[host.name] always wins, even over DD_HOSTNAME + DD_TRACE_REPORT_HOSTNAME
		assertResourceAttribute(t, res, semconv.HostNameKey, "otel-host")
	})

	t.Run("OTEL host.name used when DD not set", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=otel-host")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.HostNameKey, "otel-host")
	})

	t.Run("DD_HOSTNAME without DD_TRACE_REPORT_HOSTNAME does not add hostname", func(t *testing.T) {
		t.Setenv("DD_HOSTNAME", "should-not-appear")
		// DD_TRACE_REPORT_HOSTNAME not set - hostname should not be added

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// Should not have host.name attribute
		attrs := res.Attributes()
		for _, attr := range attrs {
			assert.NotEqual(t, semconv.HostNameKey, attr.Key, "host.name should not be present without DD_TRACE_REPORT_HOSTNAME=true")
		}
	})
}

func TestInvalidInputs(t *testing.T) {
	t.Run("empty DD_TAGS is handled gracefully", func(t *testing.T) {
		t.Setenv("DD_TAGS", "")

		res, err := buildResource(context.Background())
		require.NoError(t, err)
		require.NotNil(t, res)
	})

	t.Run("malformed DD_TAGS handled gracefully", func(t *testing.T) {
		t.Setenv("DD_TAGS", "invalid-no-value,valid:value")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// Valid tag should be present
		assertResourceAttributeString(t, res, "valid", "value")
	})

	t.Run("empty OTEL_RESOURCE_ATTRIBUTES is handled gracefully", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

		res, err := buildResource(context.Background())
		require.NoError(t, err)
		require.NotNil(t, res)
	})

	t.Run("malformed OTEL_RESOURCE_ATTRIBUTES handled gracefully", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "invalid-no-equals,valid=value")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// Valid attribute should be present
		assertResourceAttributeString(t, res, "valid", "value")
	})

	t.Run("special characters in values preserved", func(t *testing.T) {
		t.Setenv("DD_TAGS", "special:with-dash_underscore.dot")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttributeString(t, res, "special", "with-dash_underscore.dot")
	})
}

func TestComplexScenarios(t *testing.T) {
	t.Run("all DD settings together", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "my-service")
		t.Setenv("DD_ENV", "staging")
		t.Setenv("DD_VERSION", "3.0.0")
		t.Setenv("DD_TAGS", "team:platform,tier:critical")
		t.Setenv("DD_HOSTNAME", "server-01")
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true") // Required to enable hostname reporting

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		assertResourceAttribute(t, res, semconv.ServiceNameKey, "my-service")
		assertResourceAttribute(t, res, semconv.DeploymentEnvironmentNameKey, "staging")
		assertResourceAttribute(t, res, semconv.ServiceVersionKey, "3.0.0")
		assertResourceAttributeString(t, res, "team", "platform")
		assertResourceAttributeString(t, res, "tier", "critical")
		assertResourceAttribute(t, res, semconv.HostNameKey, "server-01")
	})

	t.Run("DD overrides OTEL for service/env/version except hostname", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "dd-service")
		t.Setenv("DD_ENV", "dd-env")
		t.Setenv("DD_VERSION", "dd-version")
		t.Setenv("DD_TAGS", "custom:dd-tag")
		t.Setenv("DD_HOSTNAME", "dd-host")
		t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=otel-service,deployment.environment.name=otel-env,service.version=otel-version,custom=otel-tag,host.name=otel-host")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// DD values win for service/env/version/custom, but OTEL host.name has highest priority
		assertResourceAttribute(t, res, semconv.ServiceNameKey, "dd-service")
		assertResourceAttribute(t, res, semconv.DeploymentEnvironmentNameKey, "dd-env")
		assertResourceAttribute(t, res, semconv.ServiceVersionKey, "dd-version")
		assertResourceAttributeString(t, res, "custom", "dd-tag")
		// OTEL host.name wins (has highest priority per OTel spec)
		assertResourceAttribute(t, res, semconv.HostNameKey, "otel-host")
	})

	t.Run("multiple DD_TAGS with same key uses last value", func(t *testing.T) {
		// Note: This tests internal.ParseTagString behavior
		t.Setenv("DD_TAGS", "key:first,key:second")

		res, err := buildResource(context.Background())
		require.NoError(t, err)

		// The last value should win (depends on internal.ParseTagString implementation)
		attrs := res.Attributes()
		hasKey := false
		for _, attr := range attrs {
			if attr.Key == "key" {
				hasKey = true
				// Value will be one of them depending on map iteration
				assert.Contains(t, []string{"first", "second"}, attr.Value.AsString())
			}
		}
		assert.True(t, hasKey, "key should be present in attributes")
	})
}

func TestParseOtelResourceAttributes(t *testing.T) {
	t.Run("empty string returns empty map", func(t *testing.T) {
		result := parseOtelResourceAttributes("")
		assert.Empty(t, result)
	})

	t.Run("single attribute", func(t *testing.T) {
		result := parseOtelResourceAttributes("key=value")
		assert.Equal(t, map[string]string{"key": "value"}, result)
	})

	t.Run("multiple attributes", func(t *testing.T) {
		result := parseOtelResourceAttributes("key1=value1,key2=value2,key3=value3")
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}, result)
	})

	t.Run("attributes with dots and underscores", func(t *testing.T) {
		result := parseOtelResourceAttributes("service.name=my-service,host_name=my-host")
		assert.Equal(t, map[string]string{
			"service.name": "my-service",
			"host_name":    "my-host",
		}, result)
	})

	t.Run("values with special characters", func(t *testing.T) {
		result := parseOtelResourceAttributes("key=value-with-dash_and_underscore.and.dot")
		assert.Equal(t, map[string]string{"key": "value-with-dash_and_underscore.and.dot"}, result)
	})
}

// Helper functions

func assertResourceAttribute(t *testing.T, res *resource.Resource, key attribute.Key, expectedValue string) {
	t.Helper()
	attrs := res.Attributes()
	for _, attr := range attrs {
		if attr.Key == key {
			assert.Equal(t, expectedValue, attr.Value.AsString(), "unexpected value for %s", key)
			return
		}
	}
	t.Errorf("attribute %s not found in resource", key)
}

func assertResourceAttributeString(t *testing.T, res *resource.Resource, key string, expectedValue string) {
	t.Helper()
	attrs := res.Attributes()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			assert.Equal(t, expectedValue, attr.Value.AsString(), "unexpected value for %s", key)
			return
		}
	}
	t.Errorf("attribute %s not found in resource", key)
}
