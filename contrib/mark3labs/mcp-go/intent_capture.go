// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"slices"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const ddtraceKey = "ddtrace"

const intentPrompt string = "Briefly describe the wider context task, and why this tool was chosen. Omit argument values, PII/secrets. Use English."

func ddtraceSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"intent": map[string]any{
				"type":        "string",
				"description": intentPrompt,
			},
		},
		"required":             []string{"intent"},
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
		t.InputSchema.Properties[ddtraceKey] = ddtraceSchema()

		// Mark ddtrace as required (idempotent)
		if !slices.Contains(t.InputSchema.Required, ddtraceKey) {
			t.InputSchema.Required = append(t.InputSchema.Required, ddtraceKey)
		}
	}
}

// Removing tracing parameters from the tool call request so its not sent to the tool.
// This must be registered before the tool handler middleware, so that the span is available.
// This must be registered after any user-defined middleware so that it is not visible to them.
var processAndRemoveDdtraceToolMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if m, ok := request.Params.Arguments.(map[string]any); ok && m != nil {
			if ddtraceVal, has := m[ddtraceKey]; has {
				if ddtraceMap, ok := ddtraceVal.(map[string]any); ok {
					processDdtrace(ctx, ddtraceMap)
				} else if instr != nil && instr.Logger() != nil {
					instr.Logger().Warn("mcp-go intent capture: ddtrace value is not a map")
				}
				delete(m, ddtraceKey)
				request.Params.Arguments = m
			}
		}

		return next(ctx, request)
	}
}

func processDdtrace(ctx context.Context, m map[string]any) {
	if m == nil {
		return
	}

	intentVal, exists := m["intent"]
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
	// TODO: Add fields to toolSpan to annotate intent
	_ = toolSpan
}
