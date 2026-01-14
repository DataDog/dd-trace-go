// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

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

	// OTel environment variable names
	envOtelResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
)

// buildResource creates an OpenTelemetry resource for logs with Datadog-specific attributes.
//
// Precedence rule (critical): Datadog settings win over OTEL_RESOURCE_ATTRIBUTES
// Implementation:
// 1. Parse OTEL_RESOURCE_ATTRIBUTES into a map first (base layer)
// 2. Overlay Datadog-derived attributes on top (overwrite conflicts):
//   - DD_SERVICE → service.name
//   - DD_ENV → deployment.environment
//   - DD_VERSION → service.version
//   - DD_TAGS → convert k:v pairs into resource attributes
//
// Hostname rule:
// - host.name is only set if explicitly provided by:
//   - DD_HOSTNAME, or
//   - DD_TRACE_REPORT_HOSTNAME=true (uses DD_HOSTNAME or detected hostname), or
//   - OTEL_RESOURCE_ATTRIBUTES already includes it
//
// - Datadog hostname takes precedence over OTEL hostname if both are present
func buildResource(ctx context.Context, opts ...resource.Option) (*resource.Resource, error) {
	// Step 1: Parse OTEL_RESOURCE_ATTRIBUTES as base layer
	otelAttrs := make(map[string]string)
	if otelAttrStr := env.Get(envOtelResourceAttributes); otelAttrStr != "" {
		otelAttrs = parseOtelResourceAttributes(otelAttrStr)
	}

	// Step 2: Parse DD_TAGS
	ddTags := make(map[string]string)
	if ddTagsStr := env.Get(envDDTags); ddTagsStr != "" {
		ddTags = internal.ParseTagString(ddTagsStr)
	}

	// Step 3: Overlay Datadog attributes (these win over OTEL)
	// Start with OTEL attributes as base
	attrs := make(map[string]string)
	for k, v := range otelAttrs {
		attrs[k] = v
	}

	// Overlay DD_SERVICE → service.name
	if ddService := env.Get(envDDService); ddService != "" {
		attrs["service.name"] = ddService
	}

	// Overlay DD_ENV → deployment.environment.name
	if ddEnv := env.Get(envDDEnv); ddEnv != "" {
		attrs["deployment.environment.name"] = ddEnv
	}

	// Overlay DD_VERSION → service.version
	if ddVersion := env.Get(envDDVersion); ddVersion != "" {
		attrs["service.version"] = ddVersion
	}

	// Overlay DD_TAGS (all key-value pairs)
	for k, v := range ddTags {
		attrs[k] = v
	}

	// Step 4: Handle hostname with special rules
	// OTEL_RESOURCE_ATTRIBUTES[host.name] has highest priority - never override it
	if _, hasOtelHostname := otelAttrs["host.name"]; !hasOtelHostname {
		// OTEL didn't set hostname, so check DD settings
		hostname, shouldAddHostname := resolveHostname()
		if shouldAddHostname && hostname != "" {
			attrs["host.name"] = hostname
		}
	}

	// Step 5: Convert map to attribute.KeyValue slice
	keyValues := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		// Map known semantic convention keys
		switch k {
		case "service.name":
			keyValues = append(keyValues, semconv.ServiceName(v))
		case "deployment.environment.name":
			keyValues = append(keyValues, semconv.DeploymentEnvironmentNameKey.String(v))
		case "service.version":
			keyValues = append(keyValues, semconv.ServiceVersion(v))
		case "host.name":
			keyValues = append(keyValues, semconv.HostName(v))
		default:
			// All other attributes as-is
			keyValues = append(keyValues, attribute.String(k, v))
		}
	}

	// Merge with any user-provided resource options
	opts = append(opts, resource.WithAttributes(keyValues...))

	// Always include telemetry SDK info
	opts = append(opts, resource.WithTelemetrySDK())

	// Create the resource
	return resource.New(ctx, opts...)
}

// resolveHostname determines the hostname value and whether it should be included.
// Returns (hostname, shouldAdd) where:
//   - hostname: the resolved hostname value
//   - shouldAdd: true if hostname should be added to resource, false otherwise
//
// Hostname is ONLY added if:
// 1. DD_TRACE_REPORT_HOSTNAME=true (uses DD_HOSTNAME or detected hostname), OR
// 2. OTEL_RESOURCE_ATTRIBUTES already includes host.name (handled by caller)
//
// Just setting DD_HOSTNAME alone does NOT add hostname - it needs DD_TRACE_REPORT_HOSTNAME=true.
// This ensures hostname is only sent when explicitly enabled (privacy by default).
func resolveHostname() (string, bool) {
	// Check if DD_TRACE_REPORT_HOSTNAME is set to "true"
	reportHostname := env.Get(envDDTraceReportHostname)
	if reportHostname != "true" {
		// Hostname reporting not enabled - do not add hostname
		return "", false
	}

	// DD_TRACE_REPORT_HOSTNAME=true, so we should add hostname
	// Priority: DD_HOSTNAME → detected hostname

	// Check DD_HOSTNAME first
	if ddHostname := env.Get(envDDHostname); ddHostname != "" {
		return ddHostname, true
	}

	// Try to detect hostname
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname, true
	} else if err != nil {
		log.Warn("unable to look up hostname: %s", err.Error())
	}

	// Could not determine hostname
	return "", false
}

// parseOtelResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES string into a map.
// Format: key1=value1,key2=value2
// Invalid entries are silently ignored (best-effort parsing).
func parseOtelResourceAttributes(str string) map[string]string {
	res := make(map[string]string)
	internal.ForEachStringTag(str, internal.OtelTagsDelimeter, func(key, val string) {
		res[key] = val
	})
	return res
}
