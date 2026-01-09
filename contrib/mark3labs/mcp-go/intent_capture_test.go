// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"encoding/json"
	"testing"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntentCapture(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	var receivedArgs map[string]any
	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"),
		mcp.WithString("operation", mcp.Required(), mcp.Description("The operation to perform")),
		mcp.WithNumber("x", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("y", mcp.Required(), mcp.Description("Second number")))

	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		receivedArgs = request.Params.Arguments.(map[string]any)
		return mcp.NewToolResultText(`{"result":8}`), nil
	})

	ctx := context.Background()

	listResp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	json.Unmarshal(json.RawMessage(mustMarshal(listResp)), &listResult)

	result := listResult["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	require.Len(t, tools, 1)

	tool := tools[0].(map[string]interface{})
	schema := tool["inputSchema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})

	assert.Contains(t, props, "operation")
	assert.Contains(t, props, "x")
	assert.Contains(t, props, "y")
	assert.Contains(t, props, "telemetry")

	// Ensure telemetry is added to schema
	telemetrySchema := props["telemetry"].(map[string]interface{})
	assert.Equal(t, "object", telemetrySchema["type"])
	telemetryProps := telemetrySchema["properties"].(map[string]interface{})
	intentSchema := telemetryProps["intent"].(map[string]interface{})
	assert.Equal(t, "string", intentSchema["type"])
	assert.Equal(t, instrmcp.IntentPrompt, intentSchema["description"])

	required := schema["required"].([]interface{})
	assert.Contains(t, required, "operation")
	assert.Contains(t, required, "x")
	assert.Contains(t, required, "y")

	session := &mockSession{id: "test"}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","x":5,"y":3,"telemetry":{"intent":"test intent description"}}}}`))

	// Ensure telemetry is removed in tool call
	require.NotNil(t, receivedArgs)
	assert.Equal(t, "add", receivedArgs["operation"])
	assert.Equal(t, float64(5), receivedArgs["x"])
	assert.Equal(t, float64(3), receivedArgs["y"])
	assert.NotContains(t, receivedArgs, "telemetry")

	// Verify intent was recorded on the LLMObs span
	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])
	assert.Equal(t, "calculator", toolSpan.Name)
	assert.Contains(t, toolSpan.Meta, "intent")
	assert.Equal(t, "test intent description", toolSpan.Meta["intent"])
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
