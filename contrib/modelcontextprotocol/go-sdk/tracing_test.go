// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationSessionInitialize(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server)

	// go-sdk only assigns session ids on streamable transports.
	// Using a streamable http transport in this test allows testing session id tagging behavior.
	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	clientSession, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	sessionID := clientSession.ID()
	require.NotEmpty(t, sessionID, "session ID should be set with streamable transport")

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	taskSpan := spans[0]
	assert.Equal(t, "mcp.initialize", taskSpan.Name)
	assert.Equal(t, "task", taskSpan.Meta["span.kind"])

	assert.Contains(t, taskSpan.Tags, "client_name:test-client")
	assert.Contains(t, taskSpan.Tags, "client_version:test-client_1.0.0")
	assert.Contains(t, taskSpan.Tags, "mcp_session_id:"+sessionID)

	assert.Contains(t, taskSpan.Meta, "input")
	assert.Contains(t, taskSpan.Meta, "output")

	inputMeta := taskSpan.Meta["input"]
	assert.NotNil(t, inputMeta)
	inputWrapper := inputMeta.(map[string]any)
	inputStr := inputWrapper["value"].(string)

	var inputData map[string]any
	err = json.Unmarshal([]byte(inputStr), &inputData)
	require.NoError(t, err)
	assert.Equal(t, "initialize", inputData["method"])
	params := inputData["params"].(map[string]any)
	clientInfo := params["clientInfo"].(map[string]any)
	assert.Equal(t, "test-client", clientInfo["name"])

	outputMeta := taskSpan.Meta["output"]
	assert.NotNil(t, outputMeta)
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	outputStr := string(outputJSON)
	assert.Contains(t, outputStr, "serverInfo")
}

func TestIntegrationToolCallSuccess(t *testing.T) {
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

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "calculator",
			Description: "A simple calculator",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"operation": {Type: "string", Description: "Operation to perform"},
					"x":         {Type: "number", Description: "First operand"},
					"y":         {Type: "number", Description: "Second operand"},
				},
				Required: []string{"operation", "x", "y"},
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args CalcArgs) (*mcp.CallToolResult, any, error) {
			var result float64
			switch args.Operation {
			case "add":
				result = args.X + args.Y
			case "multiply":
				result = args.X * args.Y
			default:
				return nil, nil, fmt.Errorf("unknown operation: %s", args.Operation)
			}

			resultJSON, _ := json.Marshal(map[string]float64{"result": result})
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(resultJSON)},
				},
			}, result, nil
		},
	)

	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	clientSession, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	clientSession.ListTools(ctx, &mcp.ListToolsParams{})

	sessionID := clientSession.ID()
	require.NotEmpty(t, sessionID)

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "calculator",
		Arguments: map[string]any{
			"operation": "add",
			"x":         float64(5),
			"y":         float64(3),
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, result)

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	var initSpan, toolSpan *testtracer.LLMObsSpan
	for i := range spans {
		switch spans[i].Name {
		case "mcp.initialize":
			initSpan = &spans[i]
		case "calculator":
			toolSpan = &spans[i]
		}
	}

	require.NotNil(t, initSpan, "initialize span not found")
	require.NotNil(t, toolSpan, "tool span not found")

	// Session id must be the same between spans
	assert.Contains(t, initSpan.Tags, "mcp_session_id:"+sessionID)
	assert.Contains(t, toolSpan.Tags, "mcp_session_id:"+sessionID)

	assert.Equal(t, "calculator", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	// Verify the span is NOT marked as an error (success case)
	assert.NotContains(t, toolSpan.Meta, "error.message")
	assert.NotContains(t, toolSpan.Meta, "error.type")
	assert.NotContains(t, toolSpan.Meta, "error.stack")

	assert.Contains(t, toolSpan.Meta, "input")
	assert.Contains(t, toolSpan.Meta, "output")

	inputMeta := toolSpan.Meta["input"]
	assert.NotNil(t, inputMeta)
	inputWrapper := inputMeta.(map[string]any)
	inputStr := inputWrapper["value"].(string)

	var inputData map[string]any
	err = json.Unmarshal([]byte(inputStr), &inputData)
	require.NoError(t, err)
	assert.Equal(t, "tools/call", inputData["method"])
	params := inputData["params"].(map[string]any)
	assert.Equal(t, "calculator", params["name"])
	arguments := params["arguments"].(map[string]any)
	assert.Equal(t, "add", arguments["operation"])
	assert.Equal(t, float64(5), arguments["x"])
	assert.Equal(t, float64(3), arguments["y"])

	outputMeta := toolSpan.Meta["output"]
	assert.NotNil(t, outputMeta)
	outputWrapper := outputMeta.(map[string]any)
	outputStr := outputWrapper["value"].(string)

	var outputData map[string]any
	err = json.Unmarshal([]byte(outputStr), &outputData)
	require.NoError(t, err)

	content := outputData["content"].([]any)
	require.Len(t, content, 1)
	contentItem := content[0].(map[string]any)
	assert.Equal(t, "text", contentItem["type"])

	var resultJSON map[string]any
	err = json.Unmarshal([]byte(contentItem["text"].(string)), &resultJSON)
	require.NoError(t, err)
	const expectedResult = 8.0
	assert.Equal(t, expectedResult, resultJSON["result"])
}

func TestIntegrationToolCallError(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server)

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "error_tool",
			Description: "A tool that always errors",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: map[string]*jsonschema.Schema{},
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New("intentional test error")
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

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "error_tool",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	var toolSpan *testtracer.LLMObsSpan
	for i := range spans {
		if spans[i].Name == "error_tool" {
			toolSpan = &spans[i]
		}
	}

	require.NotNil(t, toolSpan, "tool span not found")

	assert.Equal(t, "error_tool", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "tool resulted in an error")
	assert.Contains(t, toolSpan.Meta, "error.type")
	assert.Contains(t, toolSpan.Meta, "error.stack")

	assert.Contains(t, toolSpan.Meta, "input")
	inputMeta := toolSpan.Meta["input"]
	assert.NotNil(t, inputMeta)
	inputWrapper := inputMeta.(map[string]any)
	inputStr := inputWrapper["value"].(string)

	var inputData map[string]any
	err = json.Unmarshal([]byte(inputStr), &inputData)
	require.NoError(t, err)
	assert.Equal(t, "tools/call", inputData["method"])
	params := inputData["params"].(map[string]any)
	assert.Equal(t, "error_tool", params["name"])
}

func TestIntegrationToolCallStructuredError(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	AddTracing(server)

	type ValidationArgs struct {
		Name string `json:"name"`
	}

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "validation_tool",
			Description: "A tool that returns structured error information",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", Description: "Name parameter"},
				},
				Required: []string{"name"},
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args ValidationArgs) (*mcp.CallToolResult, any, error) {
			if args.Name == "invalid" {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{
						&mcp.TextContent{Text: "invalid input: name cannot be 'invalid'"},
					},
				}, nil, nil
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Hello, %s!", args.Name)},
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

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "validation_tool",
		Arguments: map[string]any{
			"name": "invalid",
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	var toolSpan *testtracer.LLMObsSpan
	for i := range spans {
		if spans[i].Name == "validation_tool" {
			toolSpan = &spans[i]
		}
	}

	require.NotNil(t, toolSpan, "tool span not found")

	assert.Equal(t, "validation_tool", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "tool resulted in an error")
	assert.Contains(t, toolSpan.Meta, "error.type")
	assert.Contains(t, toolSpan.Meta, "error.stack")

	assert.Contains(t, toolSpan.Meta, "input")
	inputMeta := toolSpan.Meta["input"]
	assert.NotNil(t, inputMeta)
	inputWrapper := inputMeta.(map[string]any)
	inputStr := inputWrapper["value"].(string)

	var inputData map[string]any
	err = json.Unmarshal([]byte(inputStr), &inputData)
	require.NoError(t, err)
	assert.Equal(t, "tools/call", inputData["method"])
	params := inputData["params"].(map[string]any)
	assert.Equal(t, "validation_tool", params["name"])
	arguments := params["arguments"].(map[string]any)
	assert.Equal(t, "invalid", arguments["name"])

	assert.Contains(t, toolSpan.Meta, "output")

	outputMeta := toolSpan.Meta["output"]
	assert.NotNil(t, outputMeta)
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	outputStr := string(outputJSON)
	assert.Contains(t, outputStr, "invalid input")
}

// Shared helpers

// testTracer creates a testtracer with LLMObs enabled for integration tests
func testTracer(t *testing.T, opts ...testtracer.Option) *testtracer.TestTracer {
	defaultOpts := []testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-mcp-app"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	}
	allOpts := append(defaultOpts, opts...)
	tt := testtracer.Start(t, allOpts...)
	t.Cleanup(tt.Stop)
	return tt
}
