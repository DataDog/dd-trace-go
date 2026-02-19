// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"maps"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

func telemetrySchema() map[string]any {
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
func injectTelemetryListToolsHook(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
	if result == nil || result.Tools == nil {
		return
	}

	// The server reuses tools across requests. Slices and nested objects are cloned to avoid concurrent writes.
	result.Tools = slices.Clone(result.Tools)

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
		} else {
			t.InputSchema.Properties = maps.Clone(t.InputSchema.Properties)
		}

		// Insert/overwrite the telemetry property
		t.InputSchema.Properties[instrmcp.TelemetryKey] = telemetrySchema()

		// Mark telemetry as required (idempotent)
		if !slices.Contains(t.InputSchema.Required, instrmcp.TelemetryKey) {
			t.InputSchema.Required = append(slices.Clone(t.InputSchema.Required), instrmcp.TelemetryKey)
		}
	}
}

// Removing tracing parameters from the tool call request so its not sent to the tool.
// This must be registered after the tool handler middleware (mcp-go runs middleware in registration order).
// This removes the telemetry parameter before user-defined middleware or tool handlers can see it.
var processAndRemoveTelemetryToolMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if m, ok := request.Params.Arguments.(map[string]any); ok && m != nil {
			if telemetryVal, has := m[instrmcp.TelemetryKey]; has {
				if telemetryMap, ok := telemetryVal.(map[string]any); ok {
					processTelemetry(ctx, telemetryMap)
				} else if instr != nil && instr.Logger() != nil {
					instr.Logger().Warn("mcp-go intent capture: telemetry value is not a map")
				}
				delete(m, instrmcp.TelemetryKey)
			}
		}

		return next(ctx, request)
	}
}

func processTelemetry(ctx context.Context, telemetryVal map[string]any) {
	if telemetryVal == nil {
		return
	}

	intentVal, exists := telemetryVal[instrmcp.IntentKey]
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
