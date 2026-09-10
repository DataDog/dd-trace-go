// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
)

// The file contains methods for easily adding tracing and intent capture to a MCP server.

// TracingConfig holds configuration for adding tracing to an MCP server.
type TracingConfig struct {
	// Hooks allows you to provide custom hooks that will be merged with Datadog tracing hooks.
	// If nil, only Datadog tracing hooks will be added and any custom hooks provided via server.WithHooks(...) will be removed.
	// If provided, your custom hooks will be executed alongside Datadog tracing hooks.
	Hooks *server.Hooks
	// IntentCaptureEnabled enables intent capture for tool spans unconditionally.
	// This modifies the tool schemas to include a parameter for the client to provide the intent.
	// For per-request control (e.g. driven by a feature flag), use IntentCaptureEnabledFunc instead.
	IntentCaptureEnabled bool
	// IntentCaptureEnabledFunc is a per-request predicate that gates intent capture
	// at runtime. When non-nil, the intent-capture hook and middleware are registered
	// and consult the predicate on every tools/list and tools/call: if it returns
	// false, that request behaves as if intent capture were disabled (no schema
	// injection, no telemetry stripping, no intent annotation).
	//
	// If both IntentCaptureEnabled and IntentCaptureEnabledFunc are set, the
	// predicate wins. If both are unset, intent capture is disabled.
	IntentCaptureEnabledFunc func(ctx context.Context) bool
	// RedactToolOutput replaces the tool result body sent to LLMObs with a
	// fixed "[REDACTED]" string. Use this when data access control policies
	// forbid forwarding tool output to LLMObs traces.
	//
	// The result.IsError flag is still honored for span error status — only
	// the output content is redacted.
	RedactToolOutput bool
}

// intentCapturePredicate returns the runtime predicate for intent capture,
// or nil if intent capture is disabled. The predicate from
// IntentCaptureEnabledFunc takes precedence over the static
// IntentCaptureEnabled flag.
func intentCapturePredicate(options *TracingConfig) func(context.Context) bool {
	if options.IntentCaptureEnabledFunc != nil {
		return options.IntentCaptureEnabledFunc
	}
	if options.IntentCaptureEnabled {
		return func(context.Context) bool { return true }
	}
	return nil
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

		// Register the redaction middleware first (when enabled) so its ctx
		// flag is set before toolHandlerMiddleware annotates the LLMObs span.
		if options.RedactToolOutput {
			server.WithToolHandlerMiddleware(redactToolOutputMiddleware)(s)
		}

		// Register toolHandlerMiddleware first so it runs first (creates the span)
		// Note: mcp-go middleware runs in registration order (first registered runs first)
		server.WithToolHandlerMiddleware(toolHandlerMiddleware)(s)

		predicate := intentCapturePredicate(options)
		if predicate != nil {
			hooks.AddAfterListTools(injectTelemetryListToolsHookFor(predicate))
			// Register intent capture middleware second so it runs second (after span is created)
			server.WithToolHandlerMiddleware(processAndRemoveTelemetryToolMiddlewareFor(predicate))(s)
		}
	}
}
