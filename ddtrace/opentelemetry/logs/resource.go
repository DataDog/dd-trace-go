// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"os"
	"runtime"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// NewResource creates an OpenTelemetry Resource with Datadog-specific attributes
// Datadog environment variables take precedence over OpenTelemetry resource attributes
func NewResource(config *Config) *resource.Resource {
	attrs := []resource.Option{
		// Default attributes
		resource.WithAttributes(
			semconv.TelemetrySDKName("dd-trace-go"),
			semconv.TelemetrySDKVersion(version.Tag),
			semconv.TelemetrySDKLanguageGo,
		),
	}

	// Add process and runtime attributes
	attrs = append(attrs, resource.WithAttributes(
		semconv.ProcessRuntimeName(runtime.Compiler),
		semconv.ProcessRuntimeVersion(runtime.Version()),
		semconv.ProcessRuntimeDescription(runtime.Version()),
	))

	// Add host attributes if available
	if hostname, err := os.Hostname(); err == nil {
		attrs = append(attrs, resource.WithAttributes(
			semconv.HostName(hostname),
		))
	}

	// Service name: DD_SERVICE takes precedence over OTEL_SERVICE_NAME
	if service := env.Get("DD_SERVICE"); service != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.ServiceName(service),
		))
	} else if service := env.Get("OTEL_SERVICE_NAME"); service != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.ServiceName(service),
		))
	}

	// Environment: DD_ENV takes precedence
	if environment := env.Get("DD_ENV"); environment != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.DeploymentEnvironment(environment),
		))
	}

	// Version: DD_VERSION takes precedence
	if version := env.Get("DD_VERSION"); version != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.ServiceVersion(version),
		))
	}

	// Add Datadog tags as resource attributes
	if tags := env.Get("DD_TAGS"); tags != "" {
		ddAttrs := parseDDTags(tags)
		attrs = append(attrs, resource.WithAttributes(ddAttrs...))
	}

	// Add OpenTelemetry resource attributes (DD values take precedence)
	if len(config.ResourceAttributes) > 0 {
		otelAttrs := parseOTelResourceAttributes(config.ResourceAttributes)
		attrs = append(attrs, resource.WithAttributes(otelAttrs...))
	}

	// Create resource with all attributes
	res, err := resource.New(
		nil, // context - not needed for static attributes
		attrs...,
	)
	if err != nil {
		// Fall back to default resource if creation fails
		return resource.Default()
	}

	return res
}

// parseDDTags parses DD_TAGS format: "key1:value1,key2:value2"
func parseDDTags(tags string) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	for _, tag := range splitTags(tags) {
		if key, value, ok := parseTag(tag, ":"); ok {
			// Map common Datadog tags to OpenTelemetry semantic conventions
			switch key {
			case "service":
				attrs = append(attrs, semconv.ServiceName(value))
			case "env":
				attrs = append(attrs, semconv.DeploymentEnvironment(value))
			case "version":
				attrs = append(attrs, semconv.ServiceVersion(value))
			default:
				// Use custom attribute for other tags
				attrs = append(attrs, attribute.String("dd."+key, value))
			}
		}
	}

	return attrs
}

// parseOTelResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES format: "key1=value1,key2=value2"
func parseOTelResourceAttributes(resourceAttrs map[string]string) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	for key, value := range resourceAttrs {
		// Skip if we already have this attribute from Datadog (DD takes precedence)
		if isDDAttribute(key) {
			continue
		}

		attrs = append(attrs, attribute.String(key, value))
	}

	return attrs
}

// isDDAttribute checks if an OpenTelemetry attribute should be overridden by Datadog values
func isDDAttribute(key string) bool {
	switch key {
	case "service.name":
		return env.Get("DD_SERVICE") != ""
	case "deployment.environment":
		return env.Get("DD_ENV") != ""
	case "service.version":
		return env.Get("DD_VERSION") != ""
	default:
		return false
	}
}

// splitTags splits a tag string by commas, handling quoted values
func splitTags(tags string) []string {
	var result []string
	var current string
	var inQuotes bool

	for i, r := range tags {
		switch r {
		case '"':
			inQuotes = !inQuotes
			current += string(r)
		case ',':
			if inQuotes {
				current += string(r)
			} else {
				if current != "" {
					result = append(result, current)
					current = ""
				}
			}
		default:
			current += string(r)
		}

		// Add the last tag if we're at the end
		if i == len(tags)-1 && current != "" {
			result = append(result, current)
		}
	}

	return result
}

// parseTag parses a key-value pair with the given separator
func parseTag(tag, separator string) (key, value string, ok bool) {
	tag = trimQuotes(tag)

	parts := splitN(tag, separator, 2)
	if len(parts) != 2 {
		return "", "", false
	}

	key = trimQuotes(parts[0])
	value = trimQuotes(parts[1])

	return key, value, key != "" && value != ""
}

// trimQuotes removes surrounding quotes from a string
func trimQuotes(s string) string {
	s = trimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && isSpace(s[start]) {
		start++
	}

	for end > start && isSpace(s[end-1]) {
		end--
	}

	return s[start:end]
}

// isSpace checks if a character is whitespace
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// splitN splits a string by separator with a maximum number of parts
func splitN(s, sep string, n int) []string {
	if n <= 0 {
		return nil
	}

	var result []string
	for len(result) < n-1 {
		idx := indexOf(s, sep)
		if idx == -1 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}

	if s != "" {
		result = append(result, s)
	}

	return result
}

// indexOf finds the first occurrence of substr in s
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
