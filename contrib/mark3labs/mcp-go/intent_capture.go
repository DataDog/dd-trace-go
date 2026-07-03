// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"encoding/json"
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

		// mcp.ToolInputSchema only models type/properties/required/$defs;
		// for tools defined with NewToolWithRawSchema we mutate the raw
		// JSON via a generic map so keywords like additionalProperties,
		// oneOf, or patternProperties pass through verbatim.
		if t.RawInputSchema != nil {
			if newRaw, ok := injectTelemetryIntoRawSchema(t.RawInputSchema); ok {
				t.RawInputSchema = newRaw
				// mcp.NewTool sets InputSchema.Type="object" by default; keeping it
				// alongside RawInputSchema makes Tool.MarshalJSON return a schema-
				// conflict error.
				t.InputSchema = mcp.ToolInputSchema{}
			}
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

// injectTelemetryIntoRawSchema mutates a raw JSON Schema document to add the
// telemetry property and require it. Unknown top-level keywords pass through
// verbatim. Returns false when the input can't be parsed as a JSON object.
func injectTelemetryIntoRawSchema(raw json.RawMessage) (json.RawMessage, bool) {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, false
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	props, _ := schema["properties"].(map[string]any)
	props = maps.Clone(props)
	if props == nil {
		props = map[string]any{}
	}
	props[instrmcp.TelemetryKey] = telemetrySchema()
	schema["properties"] = props

	required, _ := schema["required"].([]any)
	if !slices.Contains(required, any(instrmcp.TelemetryKey)) {
		required = append(slices.Clone(required), instrmcp.TelemetryKey)
	}
	schema["required"] = required

	out, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	return out, true
}

// intentCtxKey is the context key used to stash the captured intent so tool
// handlers can forward it downstream (e.g. to a search API). Kept unexported
// to force callers through IntentFromContext.
type intentCtxKey struct{}

// IntentFromContext returns the captured intent for the current MCP tool call,
// if intent capture is enabled and the client supplied a non-empty
// telemetry.intent. The boolean is false when no intent is available.
func IntentFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(intentCtxKey{}).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// ContextWithIntent returns a copy of ctx that carries the given intent. The
// middleware uses this internally; it is exported so tests (and callers that
// fabricate their own contexts outside the standard middleware chain) can seed
// the value that IntentFromContext will later read.
func ContextWithIntent(ctx context.Context, intent string) context.Context {
	if intent == "" {
		return ctx
	}
	return context.WithValue(ctx, intentCtxKey{}, intent)
}

// Removing tracing parameters from the tool call request so its not sent to the tool.
// This must be registered after the tool handler middleware (mcp-go runs middleware in registration order).
// This removes the telemetry parameter before user-defined middleware or tool handlers can see it.
var processAndRemoveTelemetryToolMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if m, ok := request.Params.Arguments.(map[string]any); ok && m != nil {
			if telemetryVal, has := m[instrmcp.TelemetryKey]; has {
				if telemetryMap, ok := telemetryVal.(map[string]any); ok {
					if intent := extractIntent(telemetryMap); intent != "" {
						annotateIntentOnSpan(ctx, intent)
						ctx = ContextWithIntent(ctx, intent)
					}
				} else if instr != nil && instr.Logger() != nil {
					instr.Logger().Warn("mcp-go intent capture: telemetry value is not a map")
				}
				delete(m, instrmcp.TelemetryKey)
			}
		}

		return next(ctx, request)
	}
}

// extractIntent pulls the intent string out of the telemetry map supplied by
// the MCP client. It returns "" when the entry is missing, the wrong type, or
// empty — callers should treat that as "no intent" and skip further work.
func extractIntent(telemetryVal map[string]any) string {
	if telemetryVal == nil {
		return ""
	}
	intentVal, exists := telemetryVal[instrmcp.IntentKey]
	if !exists {
		return ""
	}
	intent, ok := intentVal.(string)
	if !ok {
		return ""
	}
	return intent
}

// annotateIntentOnSpan records intent on the active LLM Obs tool span, if one
// is present on ctx. It is a no-op when no span is active or the active span
// is not a tool span, so it is always safe to call.
func annotateIntentOnSpan(ctx context.Context, intent string) {
	if intent == "" {
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
	toolSpan.Annotate(llmobs.WithAnnotatedIntent(intent))
}
