// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"encoding/json"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func telemetrySchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			instrmcp.IntentKey: {
				Type:        "string",
				Description: instrmcp.IntentPrompt,
			},
		},
		Required: []string{instrmcp.IntentKey},
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
		inputSchema, ok := res.Tools[i].InputSchema.(*jsonschema.Schema)
		if !ok {
			instr.Logger().Warn("go-sdk intent capture: unexpected input schema type: %T", res.Tools[i].InputSchema)
			continue
		}

		if inputSchema.Type == "" {
			inputSchema.Type = "object"
		}
		if inputSchema.Properties == nil {
			inputSchema.Properties = map[string]*jsonschema.Schema{}
		}

		inputSchema.Properties[instrmcp.TelemetryKey] = telemetrySchema()
		inputSchema.Required = append(inputSchema.Required, instrmcp.TelemetryKey)
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
	toolSpan.Annotate(llmobs.WithIntent(intent))
}
