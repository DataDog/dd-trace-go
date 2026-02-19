// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
)

func TestIntentCapture(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server, WithIntentCapture())

	type CalcArgs struct {
		Operation string  `json:"operation"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
	}

	var receivedArgs map[string]any
	var receivedRequest *mcp.CallToolRequest
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "calculator",
			Description: "A simple calculator",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"operation": {Type: "string", Description: "The operation to perform"},
					"x":         {Type: "number", Description: "First number"},
					"y":         {Type: "number", Description: "Second number"},
				},
				Required: []string{"operation", "x", "y"},
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			receivedArgs = args
			receivedRequest = req
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"result":8}`},
				},
			}, nil, nil
		},
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	listResult, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, listResult.Tools, 1)

	tool := listResult.Tools[0]
	schemaMap, ok := tool.InputSchema.(map[string]any)
	require.True(t, ok, "expected input schema to be a map[string]any, got %T", tool.InputSchema)

	// Verify the input schema has the telemetry property added
	props := schemaMap["properties"].(map[string]any)
	assert.Contains(t, props, "operation")
	assert.Contains(t, props, "x")
	assert.Contains(t, props, "y")
	assert.Contains(t, props, "telemetry")

	telemetrySchema := props["telemetry"].(map[string]any)
	assert.Equal(t, "object", telemetrySchema["type"])
	telemetryProps := telemetrySchema["properties"].(map[string]any)
	intentSchema := telemetryProps["intent"].(map[string]any)
	assert.Equal(t, "string", intentSchema["type"])
	assert.Equal(t, instrmcp.IntentPrompt, intentSchema["description"])

	// Ensure telemetry is required, and others are not affected
	required := schemaMap["required"].([]any)
	assert.Contains(t, required, "operation")
	assert.Contains(t, required, "x")
	assert.Contains(t, required, "y")
	assert.Contains(t, required, "telemetry")

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "calculator",
		Arguments: map[string]any{
			"operation": "add",
			"x":         float64(5),
			"y":         float64(3),
			"telemetry": map[string]any{
				"intent": "test intent description",
			},
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Received arguments
	assert.Equal(t, "add", receivedArgs["operation"])
	assert.Equal(t, float64(5), receivedArgs["x"])
	assert.Equal(t, float64(3), receivedArgs["y"])
	assert.NotContains(t, receivedArgs, "telemetry")

	// Received request also does not contain telemetry
	assert.NotContains(t, receivedRequest.Params.Arguments, "telemetry")

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	var toolSpan *testtracer.LLMObsSpan
	for i := range spans {
		if spans[i].Name == "calculator" {
			toolSpan = &spans[i]
		}
	}

	require.NotNil(t, toolSpan, "tool span not found")

	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])
	assert.Equal(t, "calculator", toolSpan.Name)
	assert.Contains(t, toolSpan.Meta, "intent")
	// telemetry should not be recorded in the input
	assert.NotContains(t, toolSpan.Meta["input"], "telemetry")

	// The intent *is* captured on the span
	assert.Equal(t, "test intent description", toolSpan.Meta["intent"])
}
