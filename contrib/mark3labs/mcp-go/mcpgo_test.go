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

func TestNewToolHandlerMiddleware(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	middleware := NewToolHandlerMiddleware()
	assert.NotNil(t, middleware)
}

func TestAddServerHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	serverHooks := &server.Hooks{}
	AddServerHooks(serverHooks)

	assert.Len(t, serverHooks.OnBeforeInitialize, 1)
	assert.Len(t, serverHooks.OnAfterInitialize, 1)
	assert.Len(t, serverHooks.OnError, 1)
}

// Integration Tests

func TestIntegrationSessionInitialize(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	hooks := &server.Hooks{}
	AddServerHooks(hooks)

	srv := server.NewMCPServer("test-server", "1.0.0",
		server.WithHooks(hooks))

	ctx := context.Background()
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

func TestIntegrationToolCallSuccess(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0",
		server.WithToolHandlerMiddleware(NewToolHandlerMiddleware()))

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

	toolCallRequest := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"operation":"add","x":5,"y":3}}}`

	response := srv.HandleMessage(ctx, []byte(toolCallRequest))
	assert.NotNil(t, response)

	responseBytes, err := json.Marshal(response)
	require.NoError(t, err)

	var resp map[string]interface{}
	err = json.Unmarshal(responseBytes, &resp)
	require.NoError(t, err)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"])

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	toolSpan := spans[0]
	assert.Equal(t, "calculator", toolSpan.Name)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])

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

func TestIntegrationToolCallError(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	srv := server.NewMCPServer("test-server", "1.0.0",
		server.WithToolHandlerMiddleware(NewToolHandlerMiddleware()))

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

	assert.Contains(t, toolSpan.Meta, "error.message")
	assert.Contains(t, toolSpan.Meta["error.message"], "intentional test error")
	assert.Contains(t, toolSpan.Meta, "error.type")
	assert.Contains(t, toolSpan.Meta, "error.stack")

	assert.Contains(t, toolSpan.Meta, "input")
}

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
