// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Config holds the configuration for OpenTelemetry Logs support
type Config struct {
	// Enabled determines if OTLP logs export is enabled
	Enabled bool

	// Endpoint is the OTLP logs endpoint URL
	Endpoint string

	// Headers contains additional headers to send with OTLP requests
	Headers map[string]string

	// Timeout is the timeout for OTLP export requests
	Timeout time.Duration

	// Protocol specifies the OTLP protocol (grpc, http/protobuf, http/json)
	Protocol string

	// ResourceAttributes contains additional resource attributes
	ResourceAttributes map[string]string
}

// NewConfig creates a new Config with values from environment variables
func NewConfig() *Config {
	cfg := &Config{
		Enabled:            internal.BoolEnv("DD_LOGS_OTEL_ENABLED", false),
		Timeout:            internal.DurationEnv("OTEL_EXPORTER_OTLP_TIMEOUT", 10*time.Second),
		Protocol:           getProtocol(),
		Headers:            parseHeaders(),
		ResourceAttributes: parseResourceAttributes(),
	}

	// Set endpoint with precedence: logs-specific > general
	cfg.Endpoint = getEndpoint()

	return cfg
}

// getProtocol determines the OTLP protocol to use
func getProtocol() string {
	// Check logs-specific protocol first
	if protocol := env.Get("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"); protocol != "" {
		return protocol
	}

	// Fall back to general protocol
	if protocol := env.Get("OTEL_EXPORTER_OTLP_PROTOCOL"); protocol != "" {
		return protocol
	}

	// Default to http/protobuf
	return "http/protobuf"
}

// getEndpoint determines the OTLP endpoint to use
func getEndpoint() string {
	// Check logs-specific endpoint first
	if endpoint := env.Get("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); endpoint != "" {
		return endpoint
	}

	// Check general endpoint and append logs path
	if endpoint := env.Get("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return appendLogsPath(endpoint)
	}

	// Default to localhost agent endpoint
	return "http://localhost:8126/v0.7/logs"
}

// appendLogsPath appends the appropriate logs path to a general OTLP endpoint
func appendLogsPath(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		log.Warn("Invalid OTLP endpoint URL %q: %v", endpoint, err)
		return endpoint
	}

	// Don't modify if it already has a logs path
	if strings.Contains(u.Path, "logs") {
		return endpoint
	}

	// Append appropriate path based on protocol
	protocol := getProtocol()
	switch protocol {
	case "grpc":
		// gRPC doesn't need path modification
		return endpoint
	case "http/protobuf", "http/json":
		if u.Path == "" || u.Path == "/" {
			u.Path = "/v1/logs"
		} else {
			u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/logs"
		}
	}

	return u.String()
}

// parseHeaders parses OTLP headers from environment variables
func parseHeaders() map[string]string {
	headers := make(map[string]string)

	// Parse logs-specific headers first
	if headersStr := env.Get("OTEL_EXPORTER_OTLP_LOGS_HEADERS"); headersStr != "" {
		parseHeaderString(headersStr, headers)
	}

	// Parse general headers (logs-specific takes precedence)
	if headersStr := env.Get("OTEL_EXPORTER_OTLP_HEADERS"); headersStr != "" {
		generalHeaders := make(map[string]string)
		parseHeaderString(headersStr, generalHeaders)
		for k, v := range generalHeaders {
			if _, exists := headers[k]; !exists {
				headers[k] = v
			}
		}
	}

	return headers
}

// parseHeaderString parses a header string in the format "key1=value1,key2=value2"
func parseHeaderString(headersStr string, headers map[string]string) {
	for _, header := range strings.Split(headersStr, ",") {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}

		parts := strings.SplitN(header, "=", 2)
		if len(parts) != 2 {
			log.Warn("Invalid header format: %q", header)
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key != "" && value != "" {
			headers[key] = value
		}
	}
}

// parseResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES
func parseResourceAttributes() map[string]string {
	attrs := make(map[string]string)

	if attrsStr := env.Get("OTEL_RESOURCE_ATTRIBUTES"); attrsStr != "" {
		for _, attr := range strings.Split(attrsStr, ",") {
			attr = strings.TrimSpace(attr)
			if attr == "" {
				continue
			}

			parts := strings.SplitN(attr, "=", 2)
			if len(parts) != 2 {
				log.Warn("Invalid resource attribute format: %q", attr)
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if key != "" && value != "" {
				attrs[key] = value
			}
		}
	}

	return attrs
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if !c.Enabled {
		return fmt.Errorf("OTLP logs export is disabled")
	}

	if c.Endpoint == "" {
		return fmt.Errorf("OTLP logs endpoint is required")
	}

	// Validate protocol
	switch c.Protocol {
	case "grpc", "http/protobuf", "http/json":
		// Valid protocols
	default:
		return fmt.Errorf("unsupported OTLP protocol: %q", c.Protocol)
	}

	// Validate endpoint URL
	if _, err := url.Parse(c.Endpoint); err != nil {
		return fmt.Errorf("invalid OTLP endpoint URL: %v", err)
	}

	return nil
}
