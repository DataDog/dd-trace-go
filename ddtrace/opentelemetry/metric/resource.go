// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
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
//   - service.name: DD_SERVICE → DD_TAGS[service] → OTEL_SERVICE_NAME → OTEL_RESOURCE_ATTRIBUTES[service.name]
//   - deployment.environment: DD_ENV → DD_TAGS[env] → OTEL_RESOURCE_ATTRIBUTES[deployment.environment]
//   - service.version: DD_VERSION → DD_TAGS[version] → OTEL_RESOURCE_ATTRIBUTES[service.version]
//   - host.name: OTEL_RESOURCE_ATTRIBUTES[host.name] (highest priority, always used if present)
//     → If DD_TRACE_REPORT_HOSTNAME="true": DD_HOSTNAME → detected hostname (os.Hostname())
//     → Otherwise: hostname is NOT added to resource
func buildDatadogResource(ctx context.Context, opts ...resource.Option) (*resource.Resource, error) {
	return buildDatadogResourceFromSnapshot(ctx, internalconfig.ResolveOTelMetricSnapshot(), opts...)
}

func buildDatadogResourceFromSnapshot(
	ctx context.Context,
	snapshot internalconfig.OTelMetricSnapshot,
	opts ...resource.Option,
) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{}

	// Parse DD_TAGS first to check for service, env, version there
	ddTags := snapshot.Tags

	// Parse OTEL_RESOURCE_ATTRIBUTES
	otelAttrs := snapshot.ResourceAttributes

	// 1. Service name priority: DD_SERVICE → DD_TAGS[service] → OTEL_SERVICE_NAME → OTEL_RESOURCE_ATTRIBUTES[service.name]
	serviceName := serviceNameFromSnapshot(snapshot, ddTags, otelAttrs)
	if serviceName != "" {
		attrs = append(attrs, semconv.ServiceName(serviceName))
	}

	// 2. Environment priority: DD_ENV → DD_TAGS[env] → OTEL_RESOURCE_ATTRIBUTES[deployment.environment]
	envName := environmentNameFromSnapshot(snapshot, ddTags, otelAttrs)
	if envName != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(envName))
	}

	// 3. Version priority: DD_VERSION → DD_TAGS[version] → OTEL_RESOURCE_ATTRIBUTES[service.version]
	version := versionFromSnapshot(snapshot, ddTags, otelAttrs)
	if version != "" {
		attrs = append(attrs, semconv.ServiceVersion(version))
	}

	// 4. Hostname: Only add if OTEL sets it OR if DD_TRACE_REPORT_HOSTNAME=true
	// Priority: OTEL_RESOURCE_ATTRIBUTES[host.name] (highest) → DD_HOSTNAME → detected hostname
	// If DD_TRACE_REPORT_HOSTNAME != "true" and no OTEL host.name, do NOT add hostname
	if snapshot.HasHostname && snapshot.Hostname != "" {
		attrs = append(attrs, semconv.HostName(snapshot.Hostname))
	}

	// 5. Add all other DD_TAGS as attributes (excluding service, env, version which were handled above)
	// Track which custom keys are set by DD_TAGS to prevent OTEL from overriding them
	ddCustomKeys := make(map[string]bool)
	for key, val := range ddTags {
		if key == "service" || key == "env" || key == "version" {
			continue // Already handled above
		}
		attrs = append(attrs, attribute.String(key, val))
		ddCustomKeys[key] = true // Track that this key is set by DD_TAGS
	}

	// 6. Add OTEL_RESOURCE_ATTRIBUTES (excluding reserved keys AND keys already set by DD_TAGS)
	// DD_TAGS has higher priority than OTEL_RESOURCE_ATTRIBUTES for custom tags
	excludeKeys := map[string]bool{
		"service.name":           true,
		"deployment.environment": true,
		"service.version":        true,
		"host.name":              true,
	}
	for key, val := range otelAttrs {
		if excludeKeys[key] {
			continue // Reserved keys already handled
		}
		if ddCustomKeys[key] {
			continue // DD_TAGS takes priority over OTEL for custom keys
		}
		attrs = append(attrs, attribute.String(key, val))
	}

	// Merge with any user-provided resource options
	opts = append(opts, resource.WithAttributes(attrs...))

	// Always include telemetry SDK info to ensure attributes field is never empty
	opts = append(opts, resource.WithTelemetrySDK())

	// Create the resource with defaults and our custom attributes
	return resource.New(ctx, opts...)
}

// serviceName returns the service name from environment variables with priority order
func serviceName(ddTags, otelAttrs map[string]string) string {
	return serviceNameFromSnapshot(internalconfig.ResolveOTelMetricSnapshot(), ddTags, otelAttrs)
}

func serviceNameFromSnapshot(snapshot internalconfig.OTelMetricSnapshot, ddTags, otelAttrs map[string]string) string {
	// DD_SERVICE has highest priority
	if snapshot.Service != "" {
		return snapshot.Service
	}
	// DD_TAGS[service]
	if v, ok := ddTags["service"]; ok && v != "" {
		return v
	}
	// OTEL_SERVICE_NAME
	if snapshot.OTelService != "" {
		return snapshot.OTelService
	}
	// OTEL_RESOURCE_ATTRIBUTES[service.name]
	if v, ok := otelAttrs["service.name"]; ok && v != "" {
		return v
	}
	return ""
}

// environmentName returns the environment name from environment variables with priority order
func environmentName(ddTags, otelAttrs map[string]string) string {
	return environmentNameFromSnapshot(internalconfig.ResolveOTelMetricSnapshot(), ddTags, otelAttrs)
}

func environmentNameFromSnapshot(snapshot internalconfig.OTelMetricSnapshot, ddTags, otelAttrs map[string]string) string {
	// DD_ENV has highest priority
	if snapshot.Environment != "" {
		return snapshot.Environment
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

// version returns the version from environment variables with priority order
func version(ddTags, otelAttrs map[string]string) string {
	return versionFromSnapshot(internalconfig.ResolveOTelMetricSnapshot(), ddTags, otelAttrs)
}

func versionFromSnapshot(snapshot internalconfig.OTelMetricSnapshot, ddTags, otelAttrs map[string]string) string {
	// DD_VERSION has highest priority
	if snapshot.Version != "" {
		return snapshot.Version
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

// hostname returns the hostname and whether it should be added to resource attributes.
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
func hostname(otelAttrs map[string]string) (string, bool) {
	snapshot := internalconfig.ResolveOTelMetricSnapshot()
	if value := otelAttrs["host.name"]; value != "" {
		return value, true
	}
	return snapshot.Hostname, snapshot.HasHostname
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
