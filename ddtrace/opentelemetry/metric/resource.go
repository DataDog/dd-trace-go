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
// - host.name: OTEL_RESOURCE_ATTRIBUTES[host.name] (highest priority, always used if present)
//              → If DD_TRACE_REPORT_HOSTNAME="true": DD_HOSTNAME → detected hostname (os.Hostname())
//              → Otherwise: hostname is NOT added to resource
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

	// 4. Hostname: Only add if OTEL sets it OR if DD_TRACE_REPORT_HOSTNAME=true
	// Priority: OTEL_RESOURCE_ATTRIBUTES[host.name] (highest) → DD_HOSTNAME → detected hostname
	// If DD_TRACE_REPORT_HOSTNAME != "true" and no OTEL host.name, do NOT add hostname
	hostname, shouldAddHostname := getHostname(otelAttrs)
	if shouldAddHostname && hostname != "" {
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

// getHostname returns the hostname and whether it should be added to resource attributes.
// Returns (hostname, shouldAdd) where:
//   - hostname: the resolved hostname value
//   - shouldAdd: true if hostname should be added to resource, false otherwise
//
// Precedence (per OTel spec):
// 1. OTEL_RESOURCE_ATTRIBUTES[host.name] - ALWAYS wins, even if DD_TRACE_REPORT_HOSTNAME=false
// 2. If DD_TRACE_REPORT_HOSTNAME="true":
//   - Use DD_HOSTNAME if set
//   - Else use detected hostname (os.Hostname)
//
// 3. Otherwise, do NOT add hostname at all
func getHostname(otelAttrs map[string]string) (string, bool) {
	// 1. OTEL_RESOURCE_ATTRIBUTES[host.name] has highest priority - always use if present
	if v, ok := otelAttrs["host.name"]; ok && v != "" {
		return v, true
	}

	// 2. Check if DD_TRACE_REPORT_HOSTNAME is explicitly set to "true"
	reportHostname := env.Get(envDDTraceReportHostname)
	if reportHostname != "true" {
		// If not explicitly "true", do NOT add hostname
		return "", false
	}

	// 3. DD_TRACE_REPORT_HOSTNAME="true" - try DD_HOSTNAME first
	if v := env.Get(envDDHostname); v != "" {
		return v, true
	}

	// 4. Fall back to detected hostname (reusing tracer logic)
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname, true
	} else if err != nil {
		log.Warn("unable to look up hostname: %s", err.Error())
	}

	// No hostname could be determined
	return "", false
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
