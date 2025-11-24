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
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	defer tt.Stop()

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

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	taskSpan := spans[0]
	assert.Equal(t, "mcp.initialize", taskSpan.Name)
	assert.Equal(t, "task", taskSpan.Meta["span.kind"])

	assert.Contains(t, taskSpan.Tags, "client_name:test-client")
	assert.Contains(t, taskSpan.Tags, "client_version:test-client_1.0.0")

	assert.Contains(t, taskSpan.Tags, "mcp_session_id:test-session-init")

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
	defer tt.Stop()

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

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	var initSpan, toolSpan *testtracer.LLMObsSpan
	for i := range spans {
		if spans[i].Name == "mcp.initialize" {
			initSpan = &spans[i]
		} else if spans[i].Name == "calculator" {
			toolSpan = &spans[i]
		}
	}

	require.NotNil(t, initSpan, "initialize span not found")
	require.NotNil(t, toolSpan, "tool span not found")

	expectedTag := "mcp_session_id:test-session-123"
	assert.Contains(t, initSpan.Tags, expectedTag)
	assert.Contains(t, toolSpan.Tags, expectedTag)

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
	defer tt.Stop()

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

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	toolSpan := spans[0]
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
	defer tt.Stop()

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

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	assert.Equal(t, "validation_tool", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

	assert.Contains(t, toolSpan.Tags, "mcp_session_id:test-session-789")

	// Verify error metadata is present
	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "tool resulted in an error")
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

func TestWithMCPServerTracingWithCustomHooks(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

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

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	taskSpan := spans[0]
	assert.Equal(t, "mcp.initialize", taskSpan.Name)
	assert.Equal(t, "task", taskSpan.Meta["span.kind"])
}

// Test helpers

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

// mockSession is a simple mock implementation of server.ClientSession for testing
type mockSession struct {
	id             string
	initialized    bool
	notificationCh chan mcp.JSONRPCNotification
}

func (m *mockSession) SessionID() string {
	return m.id
}

func (m *mockSession) Initialize() {
	m.initialized = true
	m.notificationCh = make(chan mcp.JSONRPCNotification, 10)
}

func (m *mockSession) Initialized() bool {
	return m.initialized
}

func (m *mockSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return m.notificationCh
}
