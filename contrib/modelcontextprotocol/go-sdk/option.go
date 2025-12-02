// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import "github.com/modelcontextprotocol/go-sdk/mcp"

type config struct {
	intentCaptureEnabled bool
}

type Option func(*config)

func WithIntentCapture() Option {
	return func(cfg *config) {
		cfg.intentCaptureEnabled = true
	}
}

func AddTracing(server *mcp.Server, opts ...Option) {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Middleware in run in the ordering in this slice.
	middlewares := []mcp.Middleware{tracingMiddleware}

	// Intent capture is added after tracing so that the intent can be annotated on the existing span.
	if cfg.intentCaptureEnabled {
		middlewares = append(middlewares, intentCaptureReceivingMiddleware)
	}

	server.AddReceivingMiddleware(middlewares...)
}
