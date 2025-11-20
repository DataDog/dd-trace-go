// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"github.com/mark3labs/mcp-go/server"
)

// The file contains methods for easily adding tracing to a MCP server.

// TracingConfig holds configuration for adding tracing to an MCP server.
type TracingConfig struct {
	// Hooks allows you to provide custom hooks that will be merged with Datadog tracing hooks.
	// If nil, only Datadog tracing hooks will be added and any custom hooks provided via server.WithHooks(...) will be removed.
	// If provided, your custom hooks will be executed alongside Datadog tracing hooks.
	Hooks *server.Hooks
}

// WithMCPServerTracing adds Datadog tracing to an MCP server.
// Pass this option to server.NewMCPServer to enable tracing.
//
// Do not use with `server.WithHooks(...)`, as this overwrites the hooks.
// Instead, pass custom hooks in the TracingConfig, which will be merged with tracing hooks.
//
// Usage:
//
//	// Simple usage with only tracing hooks
//	srv := server.NewMCPServer("my-server", "1.0.0",
//	    WithMCPServerTracing(nil))
//
//	// With custom hooks
//	customHooks := &server.Hooks{}
//	customHooks.AddBeforeInitialize(func(ctx context.Context, id any, request *mcp.InitializeRequest) {
//	    // Your custom logic here
//	})
//	srv := server.NewMCPServer("my-server", "1.0.0",
//	    WithMCPServerTracing(&TracingConfig{Hooks: customHooks}))
func WithMCPServerTracing(options *TracingConfig) server.ServerOption {
	return func(s *server.MCPServer) {
		if options == nil {
			options = new(TracingConfig)
		}

		hooks := options.Hooks

		// Append hooks (hooks is a private field)
		if hooks == nil {
			hooks = &server.Hooks{}
		}
		appendTracingHooks(hooks)

		server.WithHooks(hooks)(s)

		server.WithToolHandlerMiddleware(toolHandlerMiddleware)(s)
	}
}
