// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"slices"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func ddTraceSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			instrmcp.IntentKey: map[string]any{
				"type":        "string",
				"description": instrmcp.IntentPrompt,
			},
		},
		"required":             []string{instrmcp.IntentKey},
		"additionalProperties": false,
	}
}

// Injects tracing parameters into the tool list response by mutating it.
func injectDdtraceListToolsHook(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
	if result == nil || result.Tools == nil {
		return
	}

	for i := range result.Tools {
		t := &result.Tools[i]

		if t.RawInputSchema != nil {
			instr.Logger().Warn("mcp-go intent capture: raw input schema not supported")
			continue
		}

		if t.InputSchema.Type == "" {
			t.InputSchema.Type = "object"
		}
		if t.InputSchema.Properties == nil {
			t.InputSchema.Properties = map[string]any{}
		}

		// Insert/overwrite the ddtrace property
		t.InputSchema.Properties[instrmcp.DDTraceKey] = ddTraceSchema()

		// Mark ddtrace as required (idempotent)
		if !slices.Contains(t.InputSchema.Required, instrmcp.DDTraceKey) {
			t.InputSchema.Required = append(t.InputSchema.Required, instrmcp.DDTraceKey)
		}
	}
}

// Removing tracing parameters from the tool call request so its not sent to the tool.
// This must be registered after the tool handler middleware (mcp-go runs middleware in registration order).
// This removes the ddtrace parameter before user-defined middleware or tool handlers can see it.
var processAndRemoveDDTraceToolMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if m, ok := request.Params.Arguments.(map[string]any); ok && m != nil {
			if ddtraceVal, has := m[instrmcp.DDTraceKey]; has {
				if ddtraceMap, ok := ddtraceVal.(map[string]any); ok {
					processDDTrace(ctx, ddtraceMap)
				} else if instr != nil && instr.Logger() != nil {
					instr.Logger().Warn("mcp-go intent capture: ddtrace value is not a map")
				}
				delete(m, instrmcp.DDTraceKey)
			}
		}

		return next(ctx, request)
	}
}

func processDDTrace(ctx context.Context, ddTraceVal map[string]any) {
	if ddTraceVal == nil {
		return
	}

	intentVal, exists := ddTraceVal[instrmcp.IntentKey]
	if !exists {
		return
	}

	intent, ok := intentVal.(string)
	if !ok || intent == "" {
		return
	}

	span, ok := llmobs.SpanFromContext(ctx)
	if !ok {
		return
	}

	toolSpan, ok := span.AsTool()
	if !ok {
		return
	}
	toolSpan.Annotate(llmobs.WithIntent(intent))
}
