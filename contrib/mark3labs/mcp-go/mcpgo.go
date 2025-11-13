// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mcpgo // import "github.com/DataDog/dd-trace-go/contrib/mark3labs/mcp-go/v2"

import (
	"context"
	"encoding/json"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/llmobs"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageMark3LabsMCPGo)
}

func NewToolHandlerMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			toolSpan, ctx := llmobs.StartToolSpan(ctx, request.Params.Name, llmobs.WithIntegration(string(instrumentation.PackageMark3LabsMCPGo)))

			result, err := next(ctx, request)

			inputJSON, _ := json.Marshal(request)
			var outputText string
			if result != nil {
				resultJSON, _ := json.Marshal(result)
				outputText = string(resultJSON)
			}

			toolSpan.AnnotateTextIO(string(inputJSON), outputText)

			if err != nil {
				toolSpan.Finish(llmobs.WithError(err))
			} else {
				toolSpan.Finish()
			}

			return result, err
		}
	}
}
