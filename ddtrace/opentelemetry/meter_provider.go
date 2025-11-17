// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package opentelemetry

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func init() {
	// Automatically configure OpenTelemetry metrics if DD_METRICS_OTEL_ENABLED is true
	ConfigureOTelMetrics()
}

// ConfigureOTelMetrics configures the OpenTelemetry metrics environment variables
// to work with the Datadog agent. This function is called automatically when the
// package is imported (via init()), so users typically don't need to call it directly.
//
// It sets the following environment variables if they are not already set:
//   - OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: Constructed from DD_AGENT_HOST/DD_TRACE_AGENT_URL
//   - OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE: Set to "delta" for Datadog compatibility
//
// This function only takes effect when DD_METRICS_OTEL_ENABLED is true.
//
// Example usage (automatic configuration):
//
//	import (
//		_ "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry" // Automatically configures metrics
//		"go.opentelemetry.io/otel"
//		"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
//		sdkmetric "go.opentelemetry.io/otel/sdk/metric"
//	)
//
//	// Set DD_METRICS_OTEL_ENABLED=true in your environment
//	// The configuration happens automatically on import
//
//	// Initialize OpenTelemetry SDK with OTLP exporter
//	exporter, _ := otlpmetrichttp.New(context.Background())
//	provider := sdkmetric.NewMeterProvider(
//		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
//	)
//	otel.SetMeterProvider(provider)

func ConfigureOTelMetrics() {
	if !isMetricsOTelEnabled() {
		return
	}

	// Set temporality preference to delta for Datadog compatibility
	if !isEnvSet("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE") {
		setEnv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
	}

	// Configure metrics endpoint if not already set
	if !isEnvSet("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") {
		endpoint := constructMetricsEndpoint()
		if endpoint != "" {
			setEnv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", endpoint)
		}
	}
}

// isMetricsOTelEnabled checks if DD_METRICS_OTEL_ENABLED is set to true.
func isMetricsOTelEnabled() bool {
	val := env.Get("DD_METRICS_OTEL_ENABLED")
	return strings.ToLower(val) == "true" || val == "1"
}

// isEnvSet checks if an environment variable is set (to any value, including empty string).
func isEnvSet(key string) bool {
	_, exists := os.LookupEnv(key)
	return exists
}

// setEnv sets an environment variable and logs the action.
func setEnv(key, value string) {
	if err := os.Setenv(key, value); err != nil {
		log.Warn("Failed to set environment variable %s: %v", key, err)
	} else {
		log.Debug("Set %s=%s", key, value)
	}
}

// constructMetricsEndpoint constructs the OTLP metrics endpoint from Datadog agent configuration.
// It follows this priority:
//  1. Check OTEL_EXPORTER_OTLP_ENDPOINT (general OTLP endpoint)
//  2. Check DD_TRACE_AGENT_URL for custom agent URLs (including unix sockets)
//  3. Check DD_AGENT_HOST for agent hostname
//  4. Default to localhost
//
// The port is determined by the protocol:
//   - gRPC: 4317
//   - HTTP: 4318 (default)
//
// For HTTP protocol, the path /v1/metrics is appended.
func constructMetricsEndpoint() string {
	// Check if general OTLP endpoint is set
	if generalEndpoint := env.Get("OTEL_EXPORTER_OTLP_ENDPOINT"); generalEndpoint != "" {
		protocol := getProtocol()
		endpoint := strings.TrimRight(generalEndpoint, "/")
		if protocol != "grpc" {
			return endpoint + "/v1/metrics"
		}
		return endpoint
	}

	// Determine protocol
	protocol := getProtocol()

	// Determine host, scheme, and port from Datadog agent configuration
	host := ""
	scheme := "http"

	// Check DD_TRACE_AGENT_URL first
	if agentURL := env.Get("DD_TRACE_AGENT_URL"); agentURL != "" {
		if parsedURL, err := url.Parse(agentURL); err == nil {
			// Handle unix sockets
			if parsedURL.Scheme == "unix" {
				// For unix sockets, return as-is
				// Note: OpenTelemetry SDKs may need special configuration for unix sockets
				log.Warn("Unix socket URLs are not directly supported by OTLP exporters. Consider using HTTP/gRPC.")
				return agentURL
			}

			if parsedURL.Host != "" {
				host = parsedURL.Host
				if parsedURL.Scheme != "" {
					scheme = parsedURL.Scheme
				}
			}
		}
	}

	// Fall back to DD_AGENT_HOST if no URL was set
	if host == "" {
		if ddHost := env.Get("DD_AGENT_HOST"); ddHost != "" {
			host = ddHost
		} else {
			host = internal.DefaultAgentHostname
		}
	}

	// Determine port based on protocol
	port := "4318" // Default HTTP port
	if protocol == "grpc" {
		port = "4317"
	}

	// If host already contains a port, use it
	if !strings.Contains(host, ":") {
		host = fmt.Sprintf("%s:%s", host, port)
	}

	// Build endpoint
	endpoint := fmt.Sprintf("%s://%s", scheme, host)

	// Add metrics path for HTTP protocol
	if protocol != "grpc" {
		endpoint += "/v1/metrics"
	}

	return endpoint
}

// getProtocol determines the OTLP protocol to use.
// Priority: OTEL_EXPORTER_OTLP_METRICS_PROTOCOL > OTEL_EXPORTER_OTLP_PROTOCOL > "http/protobuf"
func getProtocol() string {
	// Check metrics-specific protocol
	if protocol := env.Get("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"); protocol != "" {
		if protocol == "grpc" || strings.HasPrefix(protocol, "http") {
			return protocol
		}
	}

	// Check general OTLP protocol
	if protocol := env.Get("OTEL_EXPORTER_OTLP_PROTOCOL"); protocol != "" {
		if protocol == "grpc" || strings.HasPrefix(protocol, "http") {
			return protocol
		}
	}

	// Default to http/protobuf
	return "http/protobuf"
}
