// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package logs provides OpenTelemetry Logs API support for dd-trace-go.
// This package enables structured log export via the OpenTelemetry OTLP protocol
// while maintaining compatibility with Datadog's tracing and resource attributes.
//
// The package automatically correlates logs with traces using either Datadog or
// OpenTelemetry span contexts, and supports sending logs to Datadog Agent,
// OpenTelemetry Collector, or directly to Datadog's intake.
//
// Basic usage:
//
//	import "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/logs"
//
//	// Initialize OTLP logs support
//	err := logs.InitGlobal(context.Background())
//	if err != nil {
//		log.Printf("Failed to initialize OTLP logs: %v", err)
//	}
//
//	// Use with any OpenTelemetry-compatible logging library
//	logger := otellog.GetLoggerProvider().Logger("my-service")
//
// Configuration is done via environment variables:
//
//	DD_LOGS_OTEL_ENABLED=true
//	OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://localhost:8126/v0.7/logs
//	OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf
//
// The package follows the OpenTelemetry specification for OTLP v1.7.0 and
// ensures Datadog environment variables (DD_SERVICE, DD_ENV, DD_VERSION) take
// precedence over OpenTelemetry resource attributes.
package logs

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	// Package-level initialization state
	initOnce sync.Once
	initErr  error
)

// Init initializes OpenTelemetry Logs support with default configuration.
// This function should be called once during application startup.
// It reads configuration from environment variables and sets up the global logger provider.
func Init(ctx context.Context, opts ...Option) error {
	initOnce.Do(func() {
		initErr = InitGlobal(ctx, opts...)
		if initErr != nil {
			log.Warn("Failed to initialize OTLP logs: %v", initErr)
		}
	})
	return initErr
}

// Shutdown gracefully shuts down the OTLP logs system.
// This should be called during application shutdown to ensure all logs are flushed.
func Shutdown(ctx context.Context) error {
	return ShutdownGlobal(ctx)
}

// Flush forces a flush of all pending log records.
// This can be useful before application shutdown or at specific checkpoints.
func Flush(ctx context.Context) error {
	provider := GetGlobal()
	if provider != nil {
		return provider.ForceFlush(ctx)
	}
	return nil
}

// IsInitialized returns true if OTLP logs have been successfully initialized
func IsInitialized() bool {
	return GetGlobal() != nil
}

// GetConfig returns the current OTLP logs configuration
func GetConfig() *Config {
	return NewConfig()
}

// ValidateConfig validates the current OTLP logs configuration
func ValidateConfig() error {
	config := NewConfig()
	return config.Validate()
}

// TestConnection tests the connection to the OTLP logs endpoint
func TestConnection() error {
	config := NewConfig()
	return TestExporter(config)
}

// Version information
const (
	// Version of the OTLP logs implementation
	Version = "1.0.0"

	// SupportedOTLPVersion is the OTLP version this implementation supports
	SupportedOTLPVersion = "1.7.0"
)

// GetVersion returns version information about the OTLP logs implementation
func GetVersion() map[string]string {
	return map[string]string{
		"implementation_version": Version,
		"otlp_version":           SupportedOTLPVersion,
		"protocol_support":       "grpc,http/protobuf,http/json",
	}
}
