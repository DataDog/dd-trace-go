// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	// DD environment variable names
	envDDService             = "DD_SERVICE"
	envDDEnv                 = "DD_ENV"
	envDDVersion             = "DD_VERSION"
	envDDTags                = "DD_TAGS"
	envDDHostname            = "DD_HOSTNAME"
	envDDTraceReportHostname = "DD_TRACE_REPORT_HOSTNAME"
	envDDTraceSourceHostname = "DD_TRACE_SOURCE_HOSTNAME"

	// OTel environment variable names
	envOtelServiceName        = "OTEL_SERVICE_NAME"
	envOtelResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
)

// buildDatadogResource creates an OpenTelemetry resource with Datadog-specific attributes.
// It augments the standard OTel resource with service, environment, version, hostname, and tags
// from DD environment variables, falling back to OTel environment variables when available.
//
// Priority order for each attribute:
// - service.name: DD_SERVICE → DD_TAGS[service] → OTEL_SERVICE_NAME → OTEL_RESOURCE_ATTRIBUTES[service.name]
// - deployment.environment: DD_ENV → DD_TAGS[env] → OTEL_RESOURCE_ATTRIBUTES[deployment.environment]
// - service.version: DD_VERSION → DD_TAGS[version] → OTEL_RESOURCE_ATTRIBUTES[service.version]
// - host.name: DD_TRACE_SOURCE_HOSTNAME → DD_HOSTNAME → DD_TRACE_REPORT_HOSTNAME (if "true", use os.Hostname()) → OTEL_RESOURCE_ATTRIBUTES[host.name]
func buildDatadogResource(ctx context.Context, opts ...resource.Option) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{}

	// Parse DD_TAGS first to check for service, env, version there
	ddTags := make(map[string]string)
	if ddTagsStr := env.Get(envDDTags); ddTagsStr != "" {
		ddTags = internal.ParseTagString(ddTagsStr)
	}

	// Parse OTEL_RESOURCE_ATTRIBUTES
	otelAttrs := make(map[string]string)
	if otelAttrStr := env.Get(envOtelResourceAttributes); otelAttrStr != "" {
		otelAttrs = parseOtelResourceAttributes(otelAttrStr)
	}

	// 1. Service name priority: DD_SERVICE → DD_TAGS[service] → OTEL_SERVICE_NAME → OTEL_RESOURCE_ATTRIBUTES[service.name]
	serviceName := getServiceName(ddTags, otelAttrs)
	if serviceName != "" {
		attrs = append(attrs, semconv.ServiceName(serviceName))
	}

	// 2. Environment priority: DD_ENV → DD_TAGS[env] → OTEL_RESOURCE_ATTRIBUTES[deployment.environment]
	envName := getEnvironmentName(ddTags, otelAttrs)
	if envName != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(envName))
	}

	// 3. Version priority: DD_VERSION → DD_TAGS[version] → OTEL_RESOURCE_ATTRIBUTES[service.version]
	version := getVersion(ddTags, otelAttrs)
	if version != "" {
		attrs = append(attrs, semconv.ServiceVersion(version))
	}

	// 4. Hostname priority: DD_TRACE_SOURCE_HOSTNAME → DD_HOSTNAME → DD_TRACE_REPORT_HOSTNAME → OTEL_RESOURCE_ATTRIBUTES[host.name]
	hostname := getHostname(otelAttrs)
	if hostname != "" {
		attrs = append(attrs, semconv.HostName(hostname))
	}

	// 5. Add all other DD_TAGS as attributes (excluding service, env, version which were handled above)
	for key, val := range ddTags {
		if key == "service" || key == "env" || key == "version" {
			continue // Already handled above
		}
		attrs = append(attrs, attribute.String(key, val))
	}

	// 6. Add all OTEL_RESOURCE_ATTRIBUTES (excluding ones we've already set)
	excludeKeys := map[string]bool{
		"service.name":           true,
		"deployment.environment": true,
		"service.version":        true,
		"host.name":              true,
	}
	for key, val := range otelAttrs {
		if excludeKeys[key] {
			continue
		}
		attrs = append(attrs, attribute.String(key, val))
	}

	// Merge with any user-provided resource options
	opts = append(opts, resource.WithAttributes(attrs...))

	// Create the resource with defaults and our custom attributes
	return resource.New(ctx, opts...)
}

// getServiceName returns the service name from environment variables with priority order
func getServiceName(ddTags, otelAttrs map[string]string) string {
	// DD_SERVICE has highest priority
	if v := env.Get(envDDService); v != "" {
		return v
	}
	// DD_TAGS[service]
	if v, ok := ddTags["service"]; ok && v != "" {
		return v
	}
	// OTEL_SERVICE_NAME
	if v := env.Get(envOtelServiceName); v != "" {
		return v
	}
	// OTEL_RESOURCE_ATTRIBUTES[service.name]
	if v, ok := otelAttrs["service.name"]; ok && v != "" {
		return v
	}
	return ""
}

// getEnvironmentName returns the environment name from environment variables with priority order
func getEnvironmentName(ddTags, otelAttrs map[string]string) string {
	// DD_ENV has highest priority
	if v := env.Get(envDDEnv); v != "" {
		return v
	}
	// DD_TAGS[env]
	if v, ok := ddTags["env"]; ok && v != "" {
		return v
	}
	// OTEL_RESOURCE_ATTRIBUTES[deployment.environment]
	if v, ok := otelAttrs["deployment.environment"]; ok && v != "" {
		return v
	}
	return ""
}

// getVersion returns the version from environment variables with priority order
func getVersion(ddTags, otelAttrs map[string]string) string {
	// DD_VERSION has highest priority
	if v := env.Get(envDDVersion); v != "" {
		return v
	}
	// DD_TAGS[version]
	if v, ok := ddTags["version"]; ok && v != "" {
		return v
	}
	// OTEL_RESOURCE_ATTRIBUTES[service.version]
	if v, ok := otelAttrs["service.version"]; ok && v != "" {
		return v
	}
	return ""
}

// getHostname returns the hostname from environment variables with priority order
func getHostname(otelAttrs map[string]string) string {
	// DD_TRACE_SOURCE_HOSTNAME has highest priority (can override DD_HOSTNAME)
	if v := env.Get(envDDTraceSourceHostname); v != "" {
		return v
	}
	// DD_HOSTNAME
	if v := env.Get(envDDHostname); v != "" {
		return v
	}
	// DD_TRACE_REPORT_HOSTNAME=true means use OS hostname
	if v := env.Get(envDDTraceReportHostname); v == "true" {
		if hostname, err := os.Hostname(); err == nil {
			return hostname
		} else {
			log.Warn("unable to look up hostname: %s", err.Error())
		}
	}
	// OTEL_RESOURCE_ATTRIBUTES[host.name]
	if v, ok := otelAttrs["host.name"]; ok && v != "" {
		return v
	}
	return ""
}

// parseOtelResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES string into a map.
// Format: key1=value1,key2=value2
func parseOtelResourceAttributes(str string) map[string]string {
	res := make(map[string]string)
	internal.ForEachStringTag(str, internal.OtelTagsDelimeter, func(key, val string) {
		res[key] = val
	})
	return res
}

// cleanResourceAttributes removes any git metadata tags that shouldn't be in resource attributes
func cleanResourceAttributes(attrs map[string]string) {
	internal.CleanGitMetadataTags(attrs)
}
