// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/log"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var (
	// globalLoggerProvider holds the singleton LoggerProvider instance
	globalLoggerProvider        *sdklog.LoggerProvider
	globalLoggerProviderWrapper otellog.LoggerProvider
	globalLoggerProviderOnce    sync.Once
	globalLoggerProviderMu      sync.Mutex
)

// InitGlobalLoggerProvider initializes a global OTel LoggerProvider
// configured with Datadog-specific defaults.
//
// It creates:
// - A resource with Datadog and OTEL resource attributes (with DD precedence)
// - A BatchLogRecordProcessor configured with BLRP environment variables
// - An OTLP exporter (HTTP or gRPC) configured with DD agent endpoint resolution
//
// The LoggerProvider can be accessed via GetGlobalLoggerProvider() and should be
// used by OTel log bridges and logging integrations.
//
// This function is idempotent - calling it multiple times will return the same
// LoggerProvider instance. To create a new LoggerProvider, call ShutdownGlobalLoggerProvider
// first.
//
// Returns an error if LoggerProvider creation fails.
func InitGlobalLoggerProvider(ctx context.Context) error {
	var err error
	globalLoggerProviderOnce.Do(func() {
		globalLoggerProviderMu.Lock()
		defer globalLoggerProviderMu.Unlock()

		// Create resource with Datadog precedence
		resource, resourceErr := buildResource(ctx)
		if resourceErr != nil {
			err = resourceErr
			log.Error("Failed to build resource for LoggerProvider: %v", resourceErr)
			return
		}

		// Create OTLP exporter (HTTP or gRPC based on protocol)
		exporter, exporterErr := newOTLPExporter(ctx, nil, nil)
		if exporterErr != nil {
			err = exporterErr
			log.Error("Failed to create OTLP exporter for LoggerProvider: %v", exporterErr)
			return
		}

		// Create BatchLogRecordProcessor with BLRP environment variables
		processor := sdklog.NewBatchProcessor(
			exporter,
			sdklog.WithMaxQueueSize(resolveBLRPMaxQueueSize()),
			sdklog.WithExportInterval(resolveBLRPScheduleDelay()),
			sdklog.WithExportTimeout(resolveBLRPExportTimeout()),
			sdklog.WithExportMaxBatchSize(resolveBLRPMaxExportBatchSize()),
		)

		// Create LoggerProvider with resource and processor
		globalLoggerProvider = sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)

		// Create the DD-aware wrapper
		globalLoggerProviderWrapper = &ddAwareLoggerProvider{underlying: globalLoggerProvider}

		// Register telemetry configuration
		registerTelemetry()

		log.Debug("OTel LoggerProvider initialized")
	})

	return err
}

// ShutdownGlobalLoggerProvider shuts down the global LoggerProvider if it exists.
// This flushes any pending log records and cleans up resources.
//
// This function is safe to call multiple times and is idempotent.
// After shutdown, InitGlobalLoggerProvider can be called again to create a new instance.
//
// The ctx parameter can be used to set a deadline for the shutdown operation.
// If the context is canceled or times out, shutdown will abort but still mark
// the provider as shut down.
func ShutdownGlobalLoggerProvider(ctx context.Context) error {
	globalLoggerProviderMu.Lock()
	defer globalLoggerProviderMu.Unlock()

	if globalLoggerProvider == nil {
		return nil
	}

	log.Debug("Shutting down OTel LoggerProvider")
	err := globalLoggerProvider.Shutdown(ctx)
	if err != nil {
		log.Warn("Error shutting down LoggerProvider: %v", err)
	}

	// Reset the singleton state so it can be reinitialized
	globalLoggerProvider = nil
	globalLoggerProviderWrapper = nil
	globalLoggerProviderOnce = sync.Once{}

	return err
}

// GetGlobalLoggerProvider returns the global LoggerProvider instance if it has been initialized.
// Returns nil if InitGlobalLoggerProvider has not been called yet.
//
// This LoggerProvider should be used by OTel log bridges and logging integrations
// to emit logs to the Datadog Agent via OTLP.
//
// The returned LoggerProvider automatically bridges DD spans to OTel context for
// proper trace/span correlation.
func GetGlobalLoggerProvider() otellog.LoggerProvider {
	globalLoggerProviderMu.Lock()
	defer globalLoggerProviderMu.Unlock()
	return globalLoggerProviderWrapper
}

// ddAwareLoggerProvider wraps a LoggerProvider to return DD-aware loggers
// that automatically bridge DD spans to OTel context.
type ddAwareLoggerProvider struct {
	embedded.LoggerProvider
	underlying *sdklog.LoggerProvider
}

// Logger returns a DD-aware logger that automatically bridges DD spans.
func (p *ddAwareLoggerProvider) Logger(name string, options ...otellog.LoggerOption) otellog.Logger {
	underlying := p.underlying.Logger(name, options...)
	return newDDAwareLogger(underlying)
}
