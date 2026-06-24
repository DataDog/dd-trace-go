// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mcpgo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
)

func TestIntentCapture(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	var receivedArgs map[string]any
	var receivedIntent string
	var receivedIntentOK bool
	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"),
		mcp.WithString("operation", mcp.Required(), mcp.Description("The operation to perform")),
		mcp.WithNumber("x", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("y", mcp.Required(), mcp.Description("Second number")))

	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		receivedArgs = request.Params.Arguments.(map[string]any)
		receivedIntent, receivedIntentOK = IntentFromContext(ctx)
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

	// Verify intent was stashed in ctx so the handler can forward it downstream
	// (e.g. into a search API request) without re-reading the telemetry blob.
	assert.True(t, receivedIntentOK, "IntentFromContext should report a value")
	assert.Equal(t, "test intent description", receivedIntent)

	// Verify intent was recorded on the LLMObs span
	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])
	assert.Equal(t, "calculator", toolSpan.Name)
	assert.Contains(t, toolSpan.Meta, "intent")
	assert.Equal(t, "test intent description", toolSpan.Meta["intent"])
}

type enabledKey struct{}

func TestIntentCaptureEnabledFunc(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{
		IntentCaptureEnabledFunc: func(ctx context.Context) bool {
			v, _ := ctx.Value(enabledKey{}).(bool)
			return v
		},
	}))

	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"),
		mcp.WithString("operation", mcp.Required()))

	var seenArgs map[string]any
	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seenArgs = request.Params.Arguments.(map[string]any)
		return mcp.NewToolResultText(`{"ok":true}`), nil
	})

	// Disabled per ctx: schema has no telemetry; telemetry in arg passes through.
	disabledCtx := context.WithValue(context.Background(), enabledKey{}, false)
	listResp := srv.HandleMessage(disabledCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))
	props := listResult["result"].(map[string]interface{})["tools"].([]interface{})[0].(map[string]interface{})["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	assert.NotContains(t, props, "telemetry")

	session := &mockSession{id: "s1"}
	session.Initialize()
	disabledCtx = srv.WithContext(disabledCtx, session)
	srv.HandleMessage(disabledCtx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","telemetry":{"intent":"x"}}}}`))
	// Predicate false → middleware is a pass-through, so the telemetry argument is NOT stripped.
	require.NotNil(t, seenArgs)
	assert.Contains(t, seenArgs, "telemetry")

	// Enabled per ctx: schema gets telemetry; tool argument is stripped before handler.
	enabledCtx := context.WithValue(context.Background(), enabledKey{}, true)
	listResp = srv.HandleMessage(enabledCtx, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`))
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))
	props = listResult["result"].(map[string]interface{})["tools"].([]interface{})[0].(map[string]interface{})["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	assert.Contains(t, props, "telemetry")

	session2 := &mockSession{id: "s2"}
	session2.Initialize()
	enabledCtx = srv.WithContext(enabledCtx, session2)
	seenArgs = nil
	srv.HandleMessage(enabledCtx, []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","telemetry":{"intent":"x"}}}}`))
	require.NotNil(t, seenArgs)
	assert.NotContains(t, seenArgs, "telemetry")
}

func TestIntentCaptureEnabledFuncOverridesBool(t *testing.T) {
	// When both static bool and the predicate are set, the predicate wins.
	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{
		IntentCaptureEnabled:     true,
		IntentCaptureEnabledFunc: func(context.Context) bool { return false },
	}))

	tool := mcp.NewTool("t", mcp.WithDescription("t"), mcp.WithString("q", mcp.Required()))
	srv.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	listResp := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))
	props := listResult["result"].(map[string]interface{})["tools"].([]interface{})[0].(map[string]interface{})["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	assert.NotContains(t, props, "telemetry")
}

func TestIntentCaptureSkipsUIOnlyTools(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	modelTool := mcp.NewTool("model_tool",
		mcp.WithDescription("model-callable"),
		mcp.WithString("q", mcp.Required()))
	srv.AddTool(modelTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	uiTool := mcp.NewTool("ui_tool",
		mcp.WithDescription("UI-only"),
		mcp.WithString("q", mcp.Required()))
	uiTool.Meta = &mcp.Meta{AdditionalFields: map[string]any{"ui": map[string]any{"visibility": []string{"app"}}}}
	srv.AddTool(uiTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	dualTool := mcp.NewTool("dual_tool",
		mcp.WithDescription("Model and UI"),
		mcp.WithString("q", mcp.Required()))
	dualTool.Meta = &mcp.Meta{AdditionalFields: map[string]any{"ui": map[string]any{"visibility": []string{"app", "model"}}}}
	srv.AddTool(dualTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	ctx := context.Background()
	listResp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))

	tools := listResult["result"].(map[string]interface{})["tools"].([]interface{})
	require.Len(t, tools, 3)

	byName := map[string]map[string]interface{}{}
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		byName[tool["name"].(string)] = tool["inputSchema"].(map[string]interface{})
	}

	// Model-callable tools get telemetry injected.
	for _, name := range []string{"model_tool", "dual_tool"} {
		schema := byName[name]
		require.NotNil(t, schema, name)
		props := schema["properties"].(map[string]interface{})
		assert.Contains(t, props, "telemetry", "%s should have telemetry", name)
		assert.Contains(t, schema["required"].([]interface{}), "telemetry", "%s should require telemetry", name)
	}

	// UI-only tool does NOT.
	uiSchema := byName["ui_tool"]
	require.NotNil(t, uiSchema)
	props := uiSchema["properties"].(map[string]interface{})
	assert.NotContains(t, props, "telemetry")
	if req, ok := uiSchema["required"].([]interface{}); ok {
		assert.NotContains(t, req, "telemetry")
	}
}

func TestIntentFromContext(t *testing.T) {
	ctx := context.Background()

	_, ok := IntentFromContext(ctx)
	assert.False(t, ok)

	ctx2 := ContextWithIntent(ctx, "find recent errors")
	got, ok := IntentFromContext(ctx2)
	assert.True(t, ok)
	assert.Equal(t, "find recent errors", got)

	// Empty intent does not seed the context.
	ctx3 := ContextWithIntent(ctx, "")
	_, ok = IntentFromContext(ctx3)
	assert.False(t, ok)
}

func TestIntentFromContext_AbsentWhenNoTelemetry(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	var receivedIntent string
	var receivedIntentOK bool
	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"),
		mcp.WithString("operation", mcp.Required(), mcp.Description("The operation to perform")),
		mcp.WithNumber("x", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("y", mcp.Required(), mcp.Description("Second number")))

	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		receivedIntent, receivedIntentOK = IntentFromContext(ctx)
		return mcp.NewToolResultText(`{"result":8}`), nil
	})

	ctx := context.Background()
	session := &mockSession{id: "test"}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","x":5,"y":3}}}`))

	assert.False(t, receivedIntentOK, "IntentFromContext should be empty when no telemetry was supplied")
	assert.Empty(t, receivedIntent)
}

func TestIntentCaptureRawInputSchemaViaNewToolListsWithoutConflict(t *testing.T) {
	// mcp.NewTool defaults InputSchema.Type to "object"; combined with
	// WithRawInputSchema this leaves BOTH set, and Tool.MarshalJSON refuses
	// to encode a tool with both. Intent capture must clear the structured
	// schema when it keeps the raw one.
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))
	srv.AddTool(mcp.NewTool("raw_tool",
		mcp.WithDescription("raw"),
		mcp.WithRawInputSchema(json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	listResp := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))

	require.NotNil(t, listResult["result"], "tools/list returned error: %v", listResult["error"])
	tools := listResult["result"].(map[string]interface{})["tools"].([]interface{})
	require.Len(t, tools, 1)
	props := tools[0].(map[string]interface{})["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	assert.Contains(t, props, "telemetry")
}

func TestIntentCaptureRawInputSchemaPreservesUnknownFields(t *testing.T) {
	// mcp.ToolInputSchema doesn't model additionalProperties/oneOf/etc;
	// intent capture must not silently strip those when injecting telemetry.
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))
	srv.AddTool(mcp.NewToolWithRawSchema("raw_tool", "raw", json.RawMessage(`{
		"type": "object",
		"properties": {"app_id": {"type": "string"}},
		"required": ["app_id"],
		"additionalProperties": false,
		"oneOf": [{"required": ["app_id"]}]
	}`)), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	listResp := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal(mustMarshal(listResp), &listResult))
	schema := listResult["result"].(map[string]interface{})["tools"].([]interface{})[0].(map[string]interface{})["inputSchema"].(map[string]interface{})

	assert.Contains(t, schema["properties"].(map[string]interface{}), "telemetry")
	assert.Contains(t, schema["required"].([]interface{}), "telemetry")
	assert.Equal(t, false, schema["additionalProperties"])
	assert.NotNil(t, schema["oneOf"])
}

func TestIntentCaptureRawInputSchema(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	rawSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"insight_name": {"type": "string", "description": "The insight to retrieve"},
			"app_id": {"type": "string", "description": "The application ID"}
		},
		"required": ["insight_name", "app_id"]
	}`)

	rawTool := mcp.NewToolWithRawSchema("raw_tool", "A tool with raw schema", rawSchema)
	srv.AddTool(rawTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"ok":true}`), nil
	})

	ctx := context.Background()

	listResp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	var listResult map[string]interface{}
	err := json.Unmarshal(json.RawMessage(mustMarshal(listResp)), &listResult)
	require.NoError(t, err)

	result := listResult["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	require.Len(t, tools, 1)

	tool := tools[0].(map[string]interface{})
	schema := tool["inputSchema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})

	assert.Contains(t, props, "insight_name")
	assert.Contains(t, props, "app_id")
	assert.Contains(t, props, "telemetry")

	required := schema["required"].([]interface{})
	assert.Contains(t, required, "insight_name")
	assert.Contains(t, required, "app_id")
	assert.Contains(t, required, "telemetry")
}

func TestIntentCaptureConcurrentListTools(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"),
		mcp.WithString("operation", mcp.Required(), mcp.Description("The operation to perform")),
		mcp.WithNumber("x", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("y", mcp.Required(), mcp.Description("Second number")))

	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"result":8}`), nil
	})

	ctx := context.Background()

	const numGoroutines = 10
	done := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			}
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestIntentCaptureConcurrentListToolsRawInputSchema(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0", WithMCPServerTracing(&TracingConfig{IntentCaptureEnabled: true}))

	rawSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"insight_name": {"type": "string", "description": "The insight to retrieve"},
			"app_id": {"type": "string", "description": "The application ID"}
		},
		"required": ["insight_name", "app_id"]
	}`)

	rawTool := mcp.NewToolWithRawSchema("raw_tool", "A tool with raw schema", rawSchema)
	srv.AddTool(rawTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"ok":true}`), nil
	})

	ctx := context.Background()

	const numGoroutines = 10
	done := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			}
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
