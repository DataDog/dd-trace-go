// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// StartIfEnabled initializes the OTel LoggerProvider if DD_LOGS_OTEL_ENABLED=true.
// This function should be called during tracer initialization.
//
// If the feature is not enabled, this function is a no-op.
// Returns an error if initialization fails when the feature is enabled.
func StartIfEnabled(ctx context.Context) error {
	cfg := config.Get()
	if !cfg.LogsOtelEnabled() {
		log.Debug("DD_LOGS_OTEL_ENABLED=false, skipping OTel LoggerProvider initialization")
		return nil
	}

	log.Debug("DD_LOGS_OTEL_ENABLED=true, initializing OTel LoggerProvider")
	return InitGlobalLoggerProvider(ctx)
}

// StopIfEnabled shuts down the OTel LoggerProvider if it was initialized.
// This function should be called during tracer shutdown.
//
// It flushes any pending log records and cleans up resources.
// The shutdown operation will timeout after 5 seconds to avoid blocking indefinitely.
func StopIfEnabled() {
	provider := GetGlobalLoggerProvider()
	if provider == nil {
		// Not initialized, nothing to do
		return
	}

	log.Debug("Shutting down OTel LoggerProvider")

	// Use a timeout context to avoid blocking indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ShutdownGlobalLoggerProvider(ctx); err != nil {
		log.Warn("Error shutting down OTel LoggerProvider: %v", err)
	}
}
