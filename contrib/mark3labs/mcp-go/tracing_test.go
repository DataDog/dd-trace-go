// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mcpgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestToolHandlerMiddleware(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	middleware := toolHandlerMiddleware
	assert.NotNil(t, middleware)
}

func TestAddServerHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	serverHooks := &server.Hooks{}
	appendTracingHooks(serverHooks)

	assert.Len(t, serverHooks.OnBeforeInitialize, 1)
	assert.Len(t, serverHooks.OnAfterInitialize, 1)
	assert.Len(t, serverHooks.OnError, 1)
}

func TestIntegrationSessionInitialize(t *testing.T) {
	tt := testTracer(t)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(nil))

	ctx := context.Background()
	sessionID := "test-session-init"
	session := &mockSession{id: sessionID}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	initRequest := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`

	response := srv.HandleMessage(ctx, []byte(initRequest))
	assert.NotNil(t, response)

	responseBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var resp map[string]interface{}
	err = json.Unmarshal(responseBytes, &resp)
	require.NoError(t, err)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.Equal(t, float64(1), resp["id"])
	assert.NotNil(t, resp["result"])

	tracer.Flush()
	taskSpan := tt.RequireSpan(t, "mcp.initialize")
	assert.Equal(t, "mcp.initialize", taskSpan.Name)
	assert.Equal(t, "task", taskSpan.Meta["span.kind"])

	assert.Contains(t, taskSpan.Tags, "client_name:test-client")
	assert.Contains(t, taskSpan.Tags, "client_version:test-client_1.0.0")

	assert.Contains(t, taskSpan.Tags, "mcp_session_id:test-session-init")
	assert.Contains(t, taskSpan.Tags, "mcp_method:initialize")

	assert.Contains(t, taskSpan.Meta, "input")
	assert.Contains(t, taskSpan.Meta, "output")

	inputMeta := taskSpan.Meta["input"]
	assert.NotNil(t, inputMeta)
	inputJSON, err := json.Marshal(inputMeta)
	require.NoError(t, err)
	inputStr := string(inputJSON)
	assert.Contains(t, inputStr, "2024-11-05")
	assert.Contains(t, inputStr, "test-client")

	outputMeta := taskSpan.Meta["output"]
	assert.NotNil(t, outputMeta)
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	outputStr := string(outputJSON)
	assert.Contains(t, outputStr, "serverInfo")
}

// Test tool spans are recorded on a successful tool call
func TestIntegrationToolCallSuccess(t *testing.T) {
	tt := testTracer(t)

	hooks := &server.Hooks{}
	appendTracingHooks(hooks)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(nil))

	calcTool := mcp.NewTool("calculator",
		mcp.WithDescription("A simple calculator"))

	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		operation := request.GetString("operation", "")
		x := request.GetFloat("x", 0)
		y := request.GetFloat("y", 0)

		var result float64
		switch operation {
		case "add":
			result = x + y
		case "multiply":
			result = x * y
		default:
			return nil, fmt.Errorf("unknown operation: %s", operation)
		}

		resultJSON, _ := json.Marshal(map[string]float64{"result": result})
		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	ctx := context.Background()
	sessionID := "test-session-123"

	session := &mockSession{id: sessionID}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	initRequest := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	response := srv.HandleMessage(ctx, []byte(initRequest))
	assert.NotNil(t, response)

	toolCallRequest := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","x":5,"y":3}}}`

	response = srv.HandleMessage(ctx, []byte(toolCallRequest))
	assert.NotNil(t, response)

	responseBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var resp map[string]interface{}
	err = json.Unmarshal(responseBytes, &resp)
	require.NoError(t, err)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"])

	tracer.Flush()
	initSpan := tt.RequireSpan(t, "mcp.initialize")
	toolSpan := tt.RequireSpan(t, "calculator")

	expectedTag := "mcp_session_id:test-session-123"
	assert.Contains(t, initSpan.Tags, expectedTag)
	assert.Contains(t, toolSpan.Tags, expectedTag)

	// Tool span carries the session ID natively so LLMObs groups it under the session.
	assert.Equal(t, sessionID, toolSpan.SessionID)

	assert.Contains(t, toolSpan.Tags, "mcp_method:tools/call")
	assert.Contains(t, toolSpan.Tags, "mcp_tool_kind:server")
	assert.Contains(t, toolSpan.Tags, "mcp_tool:calculator")

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

	inputJSON, err := json.Marshal(inputMeta)
	require.NoError(t, err)
	inputStr := string(inputJSON)
	assert.Contains(t, inputStr, "calculator")
	assert.Contains(t, inputStr, "add")

	outputMeta := toolSpan.Meta["output"]
	assert.NotNil(t, outputMeta)

	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	outputStr := string(outputJSON)
	assert.Contains(t, outputStr, "8")
}

// Test recording of tool spans on a failed tool call
func TestIntegrationToolCallError(t *testing.T) {
	tt := testTracer(t)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(&TracingConfig{}))

	errorTool := mcp.NewTool("error_tool",
		mcp.WithDescription("A tool that always errors"))

	srv.AddTool(errorTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("intentional test error")
	})

	ctx := context.Background()
	sessionID := "test-session-456"

	session := &mockSession{id: sessionID}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	toolCallRequest := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"error_tool","arguments":{}}}`

	response := srv.HandleMessage(ctx, []byte(toolCallRequest))
	assert.NotNil(t, response)

	responseBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var resp map[string]interface{}
	err = json.Unmarshal(responseBytes, &resp)
	require.NoError(t, err)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["error"])

	tracer.Flush()
	toolSpan := tt.RequireSpan(t, "error_tool")
	assert.Equal(t, "error_tool", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	assert.Contains(t, toolSpan.Tags, "mcp_session_id:test-session-456")

	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "intentional test error")
	assert.Contains(t, toolSpan.Meta, "error.type")
	assert.Contains(t, toolSpan.Meta, "error.stack")

	assert.Contains(t, toolSpan.Meta, "input")
}

func TestIntegrationToolCallStructuredError(t *testing.T) {
	tt := testTracer(t)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(&TracingConfig{}))

	validationTool := mcp.NewTool("validation_tool",
		mcp.WithDescription("A tool that returns structured error information"))

	srv.AddTool(validationTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Simulate validation or business logic error by returning structured error
		name, err := request.RequireString("name")
		if err != nil {
			// Return structured error result using helper function
			return mcp.NewToolResultError(fmt.Sprintf("Validation failed: %v", err)), nil
		}

		// Additional validation check
		if name == "invalid" {
			// Return structured error result with custom message
			return mcp.NewToolResultError("invalid input: name cannot be 'invalid'"), nil
		}

		// Success case (not reached in this test)
		return mcp.NewToolResultText(fmt.Sprintf("Hello, %s!", name)), nil
	})

	ctx := context.Background()
	sessionID := "test-session-789"

	session := &mockSession{id: sessionID}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	// Test with missing required parameter (should trigger RequireString error)
	toolCallRequest := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"validation_tool","arguments":{}}}`

	response := srv.HandleMessage(ctx, []byte(toolCallRequest))
	assert.NotNil(t, response)

	responseBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var resp map[string]interface{}
	err = json.Unmarshal(responseBytes, &resp)
	require.NoError(t, err)
	assert.Equal(t, "2.0", resp["jsonrpc"])

	// The response should contain result (not error), but the result IsError is true
	assert.NotNil(t, resp["result"])

	tracer.Flush()
	toolSpan := tt.RequireSpan(t, "validation_tool")
	assert.Equal(t, "validation_tool", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	assert.Contains(t, toolSpan.Tags, "mcp_session_id:test-session-789")

	// Verify error metadata is present
	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "Validation failed")
	assert.Contains(t, toolSpan.Meta, "error.type")
	assert.Contains(t, toolSpan.Meta, "error.stack")

	// Verify input and output are captured
	assert.Contains(t, toolSpan.Meta, "input")
	assert.Contains(t, toolSpan.Meta, "output")

	// Verify the output contains the structured error message
	outputMeta := toolSpan.Meta["output"]
	assert.NotNil(t, outputMeta)
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	outputStr := string(outputJSON)
	assert.Contains(t, outputStr, "Validation failed")
}

func TestRedactToolOutput(t *testing.T) {
	tt := testTracer(t)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(&TracingConfig{RedactToolOutput: true}))

	calcTool := mcp.NewTool("calculator", mcp.WithDescription("A simple calculator"))
	srv.AddTool(calcTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"result":42,"secret":"hunter2"}`), nil
	})

	ctx := context.Background()
	session := &mockSession{id: "redact-session"}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"calculator","arguments":{}}}`))

	tracer.Flush()
	spans := tt.Spans()
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	outputMeta, ok := toolSpan.Meta["output"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "[REDACTED]", outputMeta["value"])
	// Sensitive content from the actual result must not leak.
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	assert.NotContains(t, string(outputJSON), "hunter2")
	assert.NotContains(t, string(outputJSON), "42")
}

func TestRedactToolOutputStillReportsIsError(t *testing.T) {
	tt := testTracer(t)

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(&TracingConfig{RedactToolOutput: true}))

	failTool := mcp.NewTool("failing", mcp.WithDescription("A failing tool"))
	srv.AddTool(failTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("permission denied: secret-detail"), nil
	})

	ctx := context.Background()
	session := &mockSession{id: "redact-err"}
	session.Initialize()
	ctx = srv.WithContext(ctx, session)

	srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"failing","arguments":{}}}`))

	tracer.Flush()
	spans := tt.Spans()
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	assert.Equal(t, "tool resulted in an error", toolSpan.Meta["error.message"])
	outputMeta, ok := toolSpan.Meta["output"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "[REDACTED]", outputMeta["value"])
	outputJSON, err := json.Marshal(outputMeta)
	require.NoError(t, err)
	assert.NotContains(t, string(outputJSON), "secret-detail")
}

func TestWithMCPServerTracingWithCustomHooks(t *testing.T) {
	tt := testTracer(t)

	customHookCalled := false
	customHooks := &server.Hooks{}
	customHooks.AddBeforeInitialize(func(ctx context.Context, id any, request *mcp.InitializeRequest) {
		customHookCalled = true
	})

	srv := server.NewMCPServer("test-server", "1.0.0",
		WithMCPServerTracing(&TracingConfig{Hooks: customHooks}))

	ctx := context.Background()
	initRequest := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`

	response := srv.HandleMessage(ctx, []byte(initRequest))
	assert.NotNil(t, response)

	assert.True(t, customHookCalled, "custom hook should have been called")

	tracer.Flush()
	taskSpan := tt.RequireSpan(t, "mcp.initialize")
	assert.Equal(t, "mcp.initialize", taskSpan.Name)
	assert.Equal(t, "task", taskSpan.Meta["span.kind"])
}

// JSON-RPC 2.0 allows only a string or a number as a request ID, but
// mcp-go does not validate the ID. A client can send an object or an array.
// A JSON true also arrives as a comparable bool, which the hooks reject
// deliberately because it is not a valid JSON-RPC ID. The tracing hooks must
// not panic for these IDs.
func TestIntegrationInitializeNonComparableID(t *testing.T) {
	for name, id := range map[string]string{
		"object": `{"x":1}`,
		"array":  `[1,2]`,
		"bool":   `true`,
	} {
		t.Run(name, func(t *testing.T) {
			testTracer(t)

			srv := server.NewMCPServer("test-server", "1.0.0",
				WithMCPServerTracing(nil))

			initRequest := `{"jsonrpc":"2.0","id":` + id + `,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`

			var response mcp.JSONRPCMessage
			require.NotPanics(t, func() {
				response = srv.HandleMessage(context.Background(), []byte(initRequest))
			})
			assert.NotNil(t, response)
		})
	}
}

func TestHooksNonComparableID(t *testing.T) {
	logger := &recordLogger{}
	testTracer(t, tracer.WithLogger(logger))

	h := newHooks()
	ctx := context.Background()
	request := &mcp.InitializeRequest{}
	for name, id := range map[string]any{
		"object": map[string]any{"x": float64(1)},
		"array":  []any{float64(1), float64(2)},
		"bool":   true,
	} {
		t.Run(name, func(t *testing.T) {
			require.NotPanics(t, func() {
				h.onBeforeInitialize(ctx, id, request)
				h.onAfterInitialize(ctx, id, request, &mcp.InitializeResult{})
				h.onError(ctx, id, mcp.MethodInitialize, request, errors.New("initialize failed"))
			})
		})
	}

	// The logger aggregates errors. tracer.Stop flushes them.
	tracer.Stop()
	logs := logger.String()
	assert.Contains(t, logs, "mcp-go: skipping initialize span: unsupported JSON-RPC request ID type")
	// The client controls the ID value, so the log must not contain it.
	assert.NotContains(t, logs, "x:1")
	assert.NotContains(t, logs, "[1 2]")
}

type recordLogger struct {
	mu   sync.Mutex
	logs []string
}

func (l *recordLogger) Log(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, msg)
}

func (l *recordLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.logs, "\n")
}

// Test helpers
