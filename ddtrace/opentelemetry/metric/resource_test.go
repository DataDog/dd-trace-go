// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

// TestBuildDatadogResource_DDService verifies that DD_SERVICE, DD_ENV, and DD_VERSION
// environment variables are correctly mapped to OTel resource attributes.
func TestBuildDatadogResource_DDService(t *testing.T) {
	t.Setenv(envDDService, "my-service")
	t.Setenv(envDDEnv, "production")
	t.Setenv(envDDVersion, "v1.2.3")

	res, err := buildDatadogResource(context.Background())
	require.NoError(t, err)
	require.NotNil(t, res)

	attrs := res.Attributes()
	var serviceName, env, version string
	for _, attr := range attrs {
		switch attr.Key {
		case semconv.ServiceNameKey:
			serviceName = attr.Value.AsString()
		case semconv.DeploymentEnvironmentNameKey:
			env = attr.Value.AsString()
		case semconv.ServiceVersionKey:
			version = attr.Value.AsString()
		}
	}

	assert.Equal(t, "my-service", serviceName)
	assert.Equal(t, "production", env)
	assert.Equal(t, "v1.2.3", version)
}

// TestBuildDatadogResource_DDTags verifies that DD_TAGS are parsed and mapped
// to OTel resource attributes, including reserved tags (service, env, version).
func TestBuildDatadogResource_DDTags(t *testing.T) {
	t.Setenv(envDDTags, "service:tag-service,env:tag-env,version:tag-version,custom:value")

	res, err := buildDatadogResource(context.Background())
	require.NoError(t, err)

	attrs := res.Attributes()
	attrMap := make(map[string]string)
	for _, attr := range attrs {
		attrMap[string(attr.Key)] = attr.Value.AsString()
	}

	assert.Equal(t, "tag-service", attrMap["service.name"])
	assert.Equal(t, "tag-env", attrMap["deployment.environment.name"])
	assert.Equal(t, "tag-version", attrMap["service.version"])
	assert.Equal(t, "value", attrMap["custom"])
}

// TestBuildDatadogResource_Priority verifies that DD_SERVICE takes priority
// over DD_TAGS[service] for service name resolution.
func TestBuildDatadogResource_Priority(t *testing.T) {
	// DD_SERVICE should take priority over DD_TAGS[service]
	t.Setenv(envDDService, "priority-service")
	t.Setenv(envDDTags, "service:tag-service")

	res, err := buildDatadogResource(context.Background())
	require.NoError(t, err)

	attrs := res.Attributes()
	var serviceName string
	for _, attr := range attrs {
		if attr.Key == semconv.ServiceNameKey {
			serviceName = attr.Value.AsString()
		}
	}

	assert.Equal(t, "priority-service", serviceName)
}

// TestBuildDatadogResource_OtelFallback verifies that OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES are used as fallbacks when DD_* vars are not set.
func TestBuildDatadogResource_OtelFallback(t *testing.T) {
	t.Setenv(envOtelServiceName, "otel-service")
	t.Setenv(envOtelResourceAttributes, "deployment.environment=otel-env,service.version=otel-version")

	res, err := buildDatadogResource(context.Background())
	require.NoError(t, err)

	attrs := res.Attributes()
	attrMap := make(map[string]string)
	for _, attr := range attrs {
		attrMap[string(attr.Key)] = attr.Value.AsString()
	}

	assert.Equal(t, "otel-service", attrMap["service.name"])
	assert.Equal(t, "otel-env", attrMap["deployment.environment.name"])
	assert.Equal(t, "otel-version", attrMap["service.version"])
}

// TestBuildDatadogResource_Hostname verifies hostname resolution priority:
// 1. OTEL_RESOURCE_ATTRIBUTES[host.name] (always wins)
// 2. DD_HOSTNAME (only if DD_TRACE_REPORT_HOSTNAME=true)
// 3. OS hostname (only if DD_TRACE_REPORT_HOSTNAME=true)
// 4. No hostname otherwise
func TestBuildDatadogResource_Hostname(t *testing.T) {
	t.Run("OTEL_RESOURCE_ATTRIBUTES host.name has highest priority", func(t *testing.T) {
		t.Setenv(envOtelResourceAttributes, "host.name=otel-host")
		t.Setenv(envDDHostname, "dd-host")
		t.Setenv(envDDTraceReportHostname, "false") // Even with false, OTEL wins

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		var hostname string
		for _, attr := range res.Attributes() {
			if attr.Key == semconv.HostNameKey {
				hostname = attr.Value.AsString()
			}
		}
		// OTEL_RESOURCE_ATTRIBUTES[host.name] should always win
		assert.Equal(t, "otel-host", hostname)
	})

	t.Run("DD_HOSTNAME with DD_TRACE_REPORT_HOSTNAME=true", func(t *testing.T) {
		t.Setenv(envDDTraceReportHostname, "true")
		t.Setenv(envDDHostname, "custom-host")

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		var hostname string
		for _, attr := range res.Attributes() {
			if attr.Key == semconv.HostNameKey {
				hostname = attr.Value.AsString()
			}
		}
		assert.Equal(t, "custom-host", hostname)
	})

	t.Run("DD_TRACE_REPORT_HOSTNAME=true without DD_HOSTNAME", func(t *testing.T) {
		t.Setenv(envDDTraceReportHostname, "true")
		// DD_HOSTNAME not set - should detect OS hostname

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		var hostname string
		for _, attr := range res.Attributes() {
			if attr.Key == semconv.HostNameKey {
				hostname = attr.Value.AsString()
			}
		}
		// Should be set to OS hostname
		osHostname, _ := os.Hostname()
		if osHostname != "" {
			assert.Equal(t, osHostname, hostname)
		}
	})

	t.Run("No hostname when DD_TRACE_REPORT_HOSTNAME not true", func(t *testing.T) {
		t.Setenv(envDDHostname, "should-not-appear")
		// DD_TRACE_REPORT_HOSTNAME not set (defaults to not reporting)

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		var hostname string
		var hasHostname bool
		for _, attr := range res.Attributes() {
			if attr.Key == semconv.HostNameKey {
				hostname = attr.Value.AsString()
				hasHostname = true
			}
		}
		// Should NOT have hostname attribute
		assert.False(t, hasHostname, "hostname should not be present when DD_TRACE_REPORT_HOSTNAME is not 'true'")
		assert.Empty(t, hostname)
	})

	t.Run("No hostname when DD_TRACE_REPORT_HOSTNAME=false", func(t *testing.T) {
		t.Setenv(envDDTraceReportHostname, "false")
		t.Setenv(envDDHostname, "should-not-appear")

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		var hasHostname bool
		for _, attr := range res.Attributes() {
			if attr.Key == semconv.HostNameKey {
				hasHostname = true
			}
		}
		// Should NOT have hostname attribute when explicitly set to false
		assert.False(t, hasHostname, "hostname should not be present when DD_TRACE_REPORT_HOSTNAME='false'")
	})
}

// TestParseOtelResourceAttributes verifies parsing of OTEL_RESOURCE_ATTRIBUTES
// format (key1=value1,key2=value2) with proper handling of spaces and empty strings.
func TestParseOtelResourceAttributes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:  "single attribute",
			input: "key1=value1",
			expected: map[string]string{
				"key1": "value1",
			},
		},
		{
			name:  "multiple attributes",
			input: "key1=value1,key2=value2,key3=value3",
			expected: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name:  "with spaces",
			input: "key1=value1, key2=value2 , key3=value3",
			expected: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name:     "empty string",
			input:    "",
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOtelResourceAttributes(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetHostname verifies the hostname() function's logic for determining
// whether to add hostname and what value to use based on env vars and OTEL attrs.
func TestGetHostname(t *testing.T) {
	tests := []struct {
		name              string
		otelAttrs         map[string]string
		envVars           map[string]string
		expectedHostname  string
		expectedShouldAdd bool
	}{
		{
			name:              "OTEL host.name always wins",
			otelAttrs:         map[string]string{"host.name": "otel-host"},
			envVars:           map[string]string{envDDTraceReportHostname: "false", envDDHostname: "dd-host"},
			expectedHostname:  "otel-host",
			expectedShouldAdd: true,
		},
		{
			name:              "DD_HOSTNAME with REPORT_HOSTNAME=true",
			otelAttrs:         map[string]string{},
			envVars:           map[string]string{envDDTraceReportHostname: "true", envDDHostname: "dd-host"},
			expectedHostname:  "dd-host",
			expectedShouldAdd: true,
		},
		{
			name:              "Detected hostname with REPORT_HOSTNAME=true",
			otelAttrs:         map[string]string{},
			envVars:           map[string]string{envDDTraceReportHostname: "true"},
			expectedHostname:  "", // Will be os.Hostname() but we can't predict it
			expectedShouldAdd: true,
		},
		{
			name:              "No hostname when REPORT_HOSTNAME not set",
			otelAttrs:         map[string]string{},
			envVars:           map[string]string{envDDHostname: "dd-host"},
			expectedHostname:  "",
			expectedShouldAdd: false,
		},
		{
			name:              "No hostname when REPORT_HOSTNAME=false",
			otelAttrs:         map[string]string{},
			envVars:           map[string]string{envDDTraceReportHostname: "false", envDDHostname: "dd-host"},
			expectedHostname:  "",
			expectedShouldAdd: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			hostVal, shouldAdd := hostname(tt.otelAttrs)

			assert.Equal(t, tt.expectedShouldAdd, shouldAdd, "shouldAdd mismatch")

			if tt.expectedHostname != "" {
				assert.Equal(t, tt.expectedHostname, hostVal, "hostname mismatch")
			} else if tt.expectedShouldAdd {
				// When we expect it to be added but can't predict the value (detected hostname)
				assert.NotEmpty(t, hostVal, "hostname should be detected but was empty")
			}
		})
	}
}

// TestDDTagsPriority verifies that DD_TAGS take priority over OTEL_RESOURCE_ATTRIBUTES
// for custom tags, while OTEL values are used for keys not present in DD_TAGS.
func TestDDTagsPriority(t *testing.T) {
	t.Run("DD_TAGS overrides OTEL_RESOURCE_ATTRIBUTES for custom tags", func(t *testing.T) {
		// DD_TAGS should win over OTEL_RESOURCE_ATTRIBUTES for custom tags
		t.Setenv(envDDTags, "foo:bar1,baz:qux1,service:my-service")
		t.Setenv(envOtelResourceAttributes, "foo=ignored_bar1,baz=ignored_qux1,service.name=ignored_service")

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		attrMap := make(map[string]string)
		for _, attr := range res.Attributes() {
			attrMap[string(attr.Key)] = attr.Value.AsString()
		}

		// DD_TAGS should win for custom tags
		assert.Equal(t, "bar1", attrMap["foo"], "DD_TAGS foo should override OTEL foo")
		assert.Equal(t, "qux1", attrMap["baz"], "DD_TAGS baz should override OTEL baz")

		// DD_SERVICE should win over OTEL service.name
		assert.Equal(t, "my-service", attrMap["service.name"])
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES used when DD_TAGS doesn't have the key", func(t *testing.T) {
		t.Setenv(envDDTags, "foo:bar1")
		t.Setenv(envOtelResourceAttributes, "foo=ignored,baz=qux_from_otel")

		res, err := buildDatadogResource(context.Background())
		require.NoError(t, err)

		attrMap := make(map[string]string)
		for _, attr := range res.Attributes() {
			attrMap[string(attr.Key)] = attr.Value.AsString()
		}

		// DD_TAGS should win for foo
		assert.Equal(t, "bar1", attrMap["foo"], "DD_TAGS foo should override OTEL foo")

		// OTEL should be used for baz (not in DD_TAGS)
		assert.Equal(t, "qux_from_otel", attrMap["baz"], "OTEL baz should be used when not in DD_TAGS")
	})
}

// TestResourceAlwaysHasAttributes verifies that the resource always has at least
// the telemetry SDK attributes (name, language, version) even when other attrs are empty.
func TestResourceAlwaysHasAttributes(t *testing.T) {
	res, err := buildDatadogResource(context.Background())
	require.NoError(t, err)
	require.NotNil(t, res)

	attrMap := make(map[string]string)
	for iter := res.Iter(); iter.Next(); {
		attr := iter.Attribute()
		attrMap[string(attr.Key)] = attr.Value.AsString()
	}

	// Should have telemetry SDK attributes (always present)
	assert.Contains(t, attrMap, "telemetry.sdk.name")
	assert.Contains(t, attrMap, "telemetry.sdk.language")
	assert.Contains(t, attrMap, "telemetry.sdk.version")

	// Should NOT have host.name when DD_TRACE_REPORT_HOSTNAME is not set
	_, hasHostname := attrMap["host.name"]
	assert.False(t, hasHostname, "should not have host.name by default")
}

// TestGetServiceName verifies service name resolution priority:
// 1. DD_SERVICE
// 2. DD_TAGS[service]
// 3. OTEL_SERVICE_NAME
// 4. OTEL_RESOURCE_ATTRIBUTES[service.name]
func TestGetServiceName(t *testing.T) {
	tests := []struct {
		name      string
		ddTags    map[string]string
		otelAttrs map[string]string
		envVars   map[string]string
		expected  string
	}{
		{
			name:     "from DD_SERVICE",
			envVars:  map[string]string{envDDService: "dd-service"},
			expected: "dd-service",
		},
		{
			name:     "from DD_TAGS",
			ddTags:   map[string]string{"service": "tag-service"},
			expected: "tag-service",
		},
		{
			name:     "from OTEL_SERVICE_NAME",
			envVars:  map[string]string{envOtelServiceName: "otel-service"},
			expected: "otel-service",
		},
		{
			name:      "from OTEL_RESOURCE_ATTRIBUTES",
			otelAttrs: map[string]string{"service.name": "otel-attr-service"},
			expected:  "otel-attr-service",
		},
		{
			name:     "priority: DD_SERVICE over tags",
			envVars:  map[string]string{envDDService: "dd-service"},
			ddTags:   map[string]string{"service": "tag-service"},
			expected: "dd-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := serviceName(tt.ddTags, tt.otelAttrs)
			assert.Equal(t, tt.expected, result)
		})
	}
}
