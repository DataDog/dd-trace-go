// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var (
	// Global logger provider instance
	globalProvider     *LoggerProvider
	globalProviderOnce sync.Once
	globalProviderMu   sync.RWMutex
)

// LoggerProvider is a Datadog-specific implementation of otellog.LoggerProvider
type LoggerProvider struct {
	embedded.LoggerProvider
	provider *sdklog.LoggerProvider
	config   *Config
	mu       sync.RWMutex
}

// NewLoggerProvider creates a new LoggerProvider with Datadog configuration
func NewLoggerProvider(ctx context.Context, opts ...Option) (*LoggerProvider, error) {
	config := NewConfig()

	// Apply options
	for _, opt := range opts {
		opt(config)
	}

	if !config.Enabled {
		log.Debug("OTLP logs export is disabled")
		return nil, nil
	}

	// Create resource with Datadog attributes
	resource := NewResource(config)

	// Create OTLP exporter
	exporter, err := NewExporter(ctx, config)
	if err != nil {
		log.Warn("Failed to create OTLP logs exporter: %v", err)
		return nil, err
	}

	// Wrap exporter with Datadog-specific functionality
	wrappedExporter := NewExporterWrapper(exporter, config)

	// Create processor (using batch processor for better performance)
	processor := sdklog.NewBatchProcessor(wrappedExporter)

	// Create logger provider
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(resource),
		sdklog.WithProcessor(processor),
	)

	return &LoggerProvider{
		provider: provider,
		config:   config,
	}, nil
}

// Logger returns a Logger with the given name and options
func (p *LoggerProvider) Logger(name string, options ...otellog.LoggerOption) otellog.Logger {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.provider == nil {
		return global.GetLoggerProvider().Logger(name, options...)
	}

	return p.provider.Logger(name, options...)
}

// Shutdown shuts down the logger provider
func (p *LoggerProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.provider == nil {
		return nil
	}

	return p.provider.Shutdown(ctx)
}

// ForceFlush forces a flush of all processors
func (p *LoggerProvider) ForceFlush(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.provider == nil {
		return nil
	}

	return p.provider.ForceFlush(ctx)
}

// Option is a function that configures a Config
type Option func(*Config)

// WithEnabled sets whether OTLP logs export is enabled
func WithEnabled(enabled bool) Option {
	return func(c *Config) {
		c.Enabled = enabled
	}
}

// WithEndpoint sets the OTLP logs endpoint
func WithEndpoint(endpoint string) Option {
	return func(c *Config) {
		c.Endpoint = endpoint
	}
}

// WithProtocol sets the OTLP protocol
func WithProtocol(protocol string) Option {
	return func(c *Config) {
		c.Protocol = protocol
	}
}

// WithHeaders sets additional headers for OTLP requests
func WithHeaders(headers map[string]string) Option {
	return func(c *Config) {
		if c.Headers == nil {
			c.Headers = make(map[string]string)
		}
		for k, v := range headers {
			c.Headers[k] = v
		}
	}
}

// WithResourceAttributes sets additional resource attributes
func WithResourceAttributes(attrs map[string]string) Option {
	return func(c *Config) {
		if c.ResourceAttributes == nil {
			c.ResourceAttributes = make(map[string]string)
		}
		for k, v := range attrs {
			c.ResourceAttributes[k] = v
		}
	}
}

// InitGlobal initializes the global logger provider if OTLP logs are enabled
func InitGlobal(ctx context.Context, opts ...Option) error {
	globalProviderOnce.Do(func() {
		provider, err := NewLoggerProvider(ctx, opts...)
		if err != nil {
			log.Warn("Failed to initialize global OTLP logs provider: %v", err)
			return
		}

		if provider != nil {
			globalProviderMu.Lock()
			globalProvider = provider
			globalProviderMu.Unlock()

			// Set as global logger provider
			global.SetLoggerProvider(provider)

			log.Debug("Initialized global OTLP logs provider")
		}
	})

	return nil
}

// GetGlobal returns the global logger provider
func GetGlobal() *LoggerProvider {
	globalProviderMu.RLock()
	defer globalProviderMu.RUnlock()
	return globalProvider
}

// ShutdownGlobal shuts down the global logger provider
func ShutdownGlobal(ctx context.Context) error {
	globalProviderMu.Lock()
	defer globalProviderMu.Unlock()

	if globalProvider != nil {
		err := globalProvider.Shutdown(ctx)
		globalProvider = nil
		return err
	}

	return nil
}

// IsEnabled returns true if OTLP logs export is enabled and configured
func IsEnabled() bool {
	config := NewConfig()
	return config.Enabled && config.Validate() == nil
}

// DisableLogInjection disables DD_LOGS_INJECTION when OTLP logs are enabled
// This prevents duplicate metadata in both log messages and OTLP payloads
func DisableLogInjection() {
	if IsEnabled() {
		// Note: This would typically set DD_LOGS_INJECTION=false
		// For now, we'll just log a warning
		log.Debug("OTLP logs enabled - consider disabling DD_LOGS_INJECTION to prevent duplicate metadata")
	}
}
