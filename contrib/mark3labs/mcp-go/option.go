// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"github.com/mark3labs/mcp-go/server"
)

// The file contains methods for easily adding tracing to a MCP server.

type TracingConfig struct {
	Hooks *server.Hooks
}

// Pass to server.NewMCPServer to add tracing to the server.
// Do not use with `server.WithHooks(...)`, as this overwrites the hooks.
// Pass custom hooks in the TracingConfig instead, which in turn is passed to server.WithHooks(...).
func WithTracing(options *TracingConfig) server.ServerOption {
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
