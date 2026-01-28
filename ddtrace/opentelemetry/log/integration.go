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

const (
	// shutdownTimeout is the maximum time to wait for the LoggerProvider to shut down.
	shutdownTimeout = 5 * time.Second
)

// Start initializes the OTel LoggerProvider if DD_LOGS_OTEL_ENABLED=true.
// This function should be called during tracer initialization.
//
// If the feature is not enabled, this function is a no-op.
// Returns an error if initialization fails when the feature is enabled.
func Start(ctx context.Context) error {
	cfg := config.Get()
	if !cfg.LogsOtelEnabled() {
		log.Debug("DD_LOGS_OTEL_ENABLED=false, skipping OTel LoggerProvider initialization")
		return nil
	}

	log.Debug("DD_LOGS_OTEL_ENABLED=true, initializing OTel LoggerProvider")
	return InitGlobalLoggerProvider(ctx)
}

// Stop shuts down the OTel LoggerProvider if it was initialized.
// This function should be called during tracer shutdown.
//
// It flushes any pending log records and cleans up resources.
// If the provider was not initialized, this is a no-op.
// The shutdown operation will timeout after 5 seconds to avoid blocking indefinitely.
func Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return ShutdownGlobalLoggerProvider(ctx)
}
