// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/llmobs"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func tracingMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/call":
			if toolReq, ok := req.(*mcp.CallToolRequest); ok {
				return traceToolCallRequest(next, ctx, method, toolReq)
			}
		case "initialize":
			return traceInitializeRequest(next, ctx, method, req)
		}
		return next(ctx, method, req)
	}
}

func traceToolCallRequest(next mcp.MethodHandler, ctx context.Context, method string, req *mcp.CallToolRequest) (mcp.Result, error) {
	toolSpan, ctx := llmobs.StartToolSpan(ctx, req.Params.Name, llmobs.WithIntegration(string(instrumentation.PackageModelContextProtocolGoSDK)))

	var result *mcp.CallToolResult
	var err error

	defer func() {
		tagWithSessionID(req, toolSpan)
		finishSpanWithIO(toolSpan, method, req, result, err)
	}()

	res, err := next(ctx, method, req)
	result, ok := res.(*mcp.CallToolResult)
	if !ok {
		instr.Logger().Warn("go-sdk: unexpected result type: %T", res)
	}

	return res, err
}

func traceInitializeRequest(next mcp.MethodHandler, ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
	taskSpan, ctx := llmobs.StartTaskSpan(ctx, "mcp.initialize", llmobs.WithIntegration(string(instrumentation.PackageModelContextProtocolGoSDK)))

	// Extract client info from params if available
	if params := req.GetParams(); params != nil {
		if initParams, ok := params.(*mcp.InitializeParams); ok {
			clientName := initParams.ClientInfo.Name
			clientVersion := initParams.ClientInfo.Version
			taskSpan.Annotate(llmobs.WithAnnotatedTags(map[string]string{"client_name": clientName, "client_version": clientName + "_" + clientVersion}))
		}
	}

	var res mcp.Result
	var err error

	defer func() {
		tagWithSessionID(req, taskSpan)
		finishSpanWithIO(taskSpan, method, req, res, err)
	}()

	res, err = next(ctx, method, req)
	return res, err
}

func tagWithSessionID(req mcp.Request, span llmobs.Span) {
	session := req.GetSession()
	if session == nil {
		return
	}
	sessionID := session.ID()
	if sessionID == "" {
		return
	}
	span.Annotate(llmobs.WithAnnotatedTags(map[string]string{"mcp_session_id": sessionID}))
}

type textIOSpan interface {
	AnnotateTextIO(input, output string, opts ...llmobs.AnnotateOption)
	Finish(opts ...llmobs.FinishSpanOption)
}

// go-sdk unmarshalls the raw jsonrpc and discards it, so that is not available for logging.
// The mcp.Request object contains extra stuff like auth and internal methods we don't want to log.
// To recreate a MCP request-like object more appropriate for json marshalling and similar to other MCP libraries, we create this struct.
type loggedInput struct {
	Method string     `json:"method"`
	Params mcp.Params `json:"params"`
}

func finishSpanWithIO[S textIOSpan](span S, method string, req mcp.Request, output mcp.Result, err error) {
	loggedInput := loggedInput{
		Method: method,
		Params: req.GetParams(),
	}

	inputJSON, marshalErr := json.Marshal(loggedInput)
	if marshalErr != nil {
		instr.Logger().Warn("go-sdk: failed to marshal input: %v", marshalErr)
	}

	var outputText string
	if output != nil {
		outputJSON, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			instr.Logger().Warn("go-sdk: failed to marshal output: %v", marshalErr)
		}
		outputText = string(outputJSON)
	}

	span.AnnotateTextIO(string(inputJSON), outputText)

	if err != nil {
		span.Finish(llmobs.WithError(err))
	} else if toolResult, ok := output.(*mcp.CallToolResult); ok && toolResult.IsError {
		// Use generic error message since details are already in the output field
		span.Finish(llmobs.WithError(errors.New("tool resulted in an error")))
	} else {
		span.Finish()
	}
}
