// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	// Integration state
	integrationMu     sync.RWMutex
	integrationActive bool
)

// Note: Integration registration is handled in init.go to avoid circular imports

// StartWithTracer initializes OTLP logs when the tracer starts
// This is called automatically by the tracer when DD_LOGS_OTEL_ENABLED=true
func StartWithTracer(ctx context.Context) error {
	integrationMu.Lock()
	defer integrationMu.Unlock()

	if integrationActive {
		return nil // Already started
	}

	config := NewConfig()
	if !config.Enabled {
		log.Debug("OTLP logs integration disabled")
		return nil
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		log.Warn("Invalid OTLP logs configuration: %v", err)
		return err
	}

	// Initialize global logger provider
	err := InitGlobal(ctx)
	if err != nil {
		log.Warn("Failed to start OTLP logs integration: %v", err)
		return err
	}

	integrationActive = true
	log.Debug("OTLP logs integration started successfully")

	// Disable log injection to prevent duplicate metadata
	DisableLogInjection()

	return nil
}

// StopWithTracer shuts down OTLP logs when the tracer stops
// This is called automatically by the tracer during shutdown
func StopWithTracer(ctx context.Context) error {
	integrationMu.Lock()
	defer integrationMu.Unlock()

	if !integrationActive {
		return nil // Not started
	}

	err := ShutdownGlobal(ctx)
	if err != nil {
		log.Warn("Error shutting down OTLP logs integration: %v", err)
	}

	integrationActive = false
	log.Debug("OTLP logs integration stopped")

	return err
}

// IsIntegrationActive returns true if the OTLP logs integration is active
func IsIntegrationActive() bool {
	integrationMu.RLock()
	defer integrationMu.RUnlock()
	return integrationActive
}

// FlushWithTracer forces a flush of OTLP logs
// This can be called during tracer flush operations
func FlushWithTracer(ctx context.Context) error {
	integrationMu.RLock()
	defer integrationMu.RUnlock()

	if !integrationActive {
		return nil
	}

	return Flush(ctx)
}

// GetIntegrationStatus returns the current status of the OTLP logs integration
func GetIntegrationStatus() map[string]interface{} {
	integrationMu.RLock()
	defer integrationMu.RUnlock()

	config := NewConfig()
	status := map[string]interface{}{
		"enabled":    config.Enabled,
		"active":     integrationActive,
		"endpoint":   config.Endpoint,
		"protocol":   config.Protocol,
		"timeout":    config.Timeout.String(),
		"configured": config.Validate() == nil,
	}

	if integrationActive {
		provider := GetGlobal()
		status["provider_initialized"] = provider != nil
	}

	return status
}
