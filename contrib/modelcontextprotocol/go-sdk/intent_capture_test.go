// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
)

func TestIntentCapturePredicate(t *testing.T) {
	// The predicate must gate per-request: when it returns false, no schema
	// injection and the telemetry argument reaches the handler.
	tt := testTracer(t)
	defer tt.Stop()
	ctx := context.Background()

	var enabled atomic.Bool
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server, WithIntentCapturePredicate(func(context.Context) bool {
		return enabled.Load()
	}))

	var receivedArgs map[string]any
	server.AddTool(&mcp.Tool{
		Name:        "tool",
		Description: "tool",
		InputSchema: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"q": {Type: "string"}}},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = json.Unmarshal(req.Params.Arguments, &receivedArgs)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	// Predicate false: schema not injected, telemetry argument passed through.
	enabled.Store(false)
	listResult, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, listResult.Tools, 1)
	if schema, ok := listResult.Tools[0].InputSchema.(map[string]any); ok {
		if props, _ := schema["properties"].(map[string]any); props != nil {
			assert.NotContains(t, props, "telemetry")
		}
	}

	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tool",
		Arguments: map[string]any{"q": "x", "telemetry": map[string]any{"intent": "ignored"}},
	})
	require.NoError(t, err)
	assert.Contains(t, receivedArgs, "telemetry", "telemetry should pass through when predicate is false")

	// Predicate true: schema injected, telemetry stripped.
	enabled.Store(true)
	listResult, err = clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	schema, ok := listResult.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, schema["properties"].(map[string]any), "telemetry")

	receivedArgs = nil
	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tool",
		Arguments: map[string]any{"q": "x", "telemetry": map[string]any{"intent": "captured"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, receivedArgs, "telemetry")
}

func TestIntentCapturePreservesUnknownSchemaKeywords(t *testing.T) {
	// *jsonschema.Schema doesn't model additionalProperties/oneOf; the map-based
	// injection must pass those through verbatim instead of dropping them.
	tt := testTracer(t)
	defer tt.Stop()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server, WithIntentCapture())

	server.AddTool(&mcp.Tool{
		Name:        "raw_tool",
		Description: "raw",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {"app_id": {"type": "string"}},
			"required": ["app_id"],
			"additionalProperties": false,
			"oneOf": [{"required": ["app_id"]}]
		}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

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

	schema, ok := listResult.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok, "expected input schema to be map[string]any, got %T", listResult.Tools[0].InputSchema)
	assert.Contains(t, schema["properties"].(map[string]any), "telemetry")
	assert.Contains(t, schema["required"].([]any), "telemetry")
	assert.Equal(t, false, schema["additionalProperties"])
	assert.NotNil(t, schema["oneOf"])
}

func TestIntentCaptureSkipsUIOnlyTools(t *testing.T) {
	// Tools whose _meta.ui.visibility omits "model" cannot be model-invoked, so
	// telemetry injection should be skipped for them.
	tt := testTracer(t)
	defer tt.Stop()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server, WithIntentCapture())

	addTool := func(name string, visibility []any) {
		tool := &mcp.Tool{
			Name:        name,
			Description: name,
			InputSchema: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"q": {Type: "string"}}},
		}
		if visibility != nil {
			tool.Meta = mcp.Meta{"ui": map[string]any{"visibility": visibility}}
		}
		server.AddTool(tool, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	}
	addTool("plain", nil)
	addTool("ui_only", []any{"app"})
	addTool("dual", []any{"model", "app"})

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
	props := func(name string) map[string]any {
		for _, tl := range listResult.Tools {
			if tl.Name != name {
				continue
			}
			schema, ok := tl.InputSchema.(map[string]any)
			if !ok {
				return nil
			}
			p, _ := schema["properties"].(map[string]any)
			return p
		}
		return nil
	}
	assert.Contains(t, props("plain"), "telemetry")
	assert.NotContains(t, props("ui_only"), "telemetry", "UI-only tool should not have telemetry injected")
	assert.Contains(t, props("dual"), "telemetry")
}

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
	assert.Equal(t, false, telemetrySchema["additionalProperties"])

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
