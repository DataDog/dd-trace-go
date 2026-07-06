// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
		"required":             []any{instrmcp.IntentKey},
		"additionalProperties": false,
	}
}

// intentCaptureReceivingMiddleware is an mcp.Server receiving middleware
// adding intent information to the tool call span.
// Intent capture works by injecting an additional required parameter on tools that the
// client agent will fill in to explain context about the task.
// The middleware records this intent on the span, and then removes it from the arguments before the tool is called.
func intentCaptureReceivingMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			// The additional parameter is added to the tool arguments returned by tools/list.
			res, err := next(ctx, method, req)
			if toolListRes, ok := res.(*mcp.ListToolsResult); ok {
				injectToolsListResponse(toolListRes)
			}
			return res, err
		case "tools/call":
			// The intent is recorded and the argument is removed.
			if toolReq, ok := req.(*mcp.CallToolRequest); ok {
				return processToolCallIntent(next, ctx, method, toolReq)
			}
		}
		return next(ctx, method, req)
	}
}

func injectToolsListResponse(res *mcp.ListToolsResult) {
	for i := range res.Tools {
		// UI-only tools cannot be invoked by the model, so injecting a
		// required telemetry parameter would be useless noise.
		if !instrmcp.IsModelCallable(res.Tools[i].Meta) {
			continue
		}
		// Round-trip the input schema through map[string]any so unknown JSON
		// Schema keywords (additionalProperties, oneOf, patternProperties, etc.)
		// that *jsonschema.Schema does not model pass through verbatim.
		schemaBytes, err := json.Marshal(res.Tools[i].InputSchema)
		if err != nil {
			instr.Logger().Warn("go-sdk intent capture: failed to marshal input schema: %v", err)
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(schemaBytes, &schema); err != nil {
			instr.Logger().Warn("go-sdk intent capture: failed to unmarshal input schema: %v", err)
			continue
		}
		if schema == nil {
			schema = map[string]any{}
		}
		if _, ok := schema["type"]; !ok {
			schema["type"] = "object"
		}

		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		props[instrmcp.TelemetryKey] = telemetrySchema()
		schema["properties"] = props

		required, _ := schema["required"].([]any)
		required = append(required, instrmcp.TelemetryKey)
		schema["required"] = required

		// Mutate a copy of the tool so the registered tool (used for server-side
		// validation in go-sdk v1.3+) is not affected.
		toolCopy := *res.Tools[i]
		toolCopy.InputSchema = schema
		res.Tools[i] = &toolCopy
	}
}

func processToolCallIntent(next mcp.MethodHandler, ctx context.Context, method string, req *mcp.CallToolRequest) (mcp.Result, error) {
	if len(req.Params.Arguments) > 0 {
		var argsMap map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &argsMap); err != nil {
			if instr != nil && instr.Logger() != nil {
				instr.Logger().Warn("go-sdk intent capture: failed to unmarshal arguments: %v", err)
			}
			return next(ctx, method, req)
		}

		if telemetryVal, has := argsMap[instrmcp.TelemetryKey]; has {
			if telemetryMap, ok := telemetryVal.(map[string]any); ok {
				annotateSpanWithIntent(ctx, telemetryMap)
			} else {
				instr.Logger().Warn("go-sdk intent capture: telemetry value is not a map")
			}

			delete(argsMap, instrmcp.TelemetryKey)

			modifiedArgs, err := json.Marshal(argsMap)
			if err != nil {
				instr.Logger().Warn("go-sdk intent capture: failed to marshal modified arguments: %v", err)
			} else {
				req.Params.Arguments = modifiedArgs
			}
		}
	}
	return next(ctx, method, req)
}

func annotateSpanWithIntent(ctx context.Context, telemetryVal map[string]any) {
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
	toolSpan.Annotate(llmobs.WithAnnotatedIntent(intent))
}
