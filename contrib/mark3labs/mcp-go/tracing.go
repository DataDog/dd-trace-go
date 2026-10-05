// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mcpgo // import "github.com/DataDog/dd-trace-go/contrib/mark3labs/mcp-go/v2"

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrmcp "github.com/DataDog/dd-trace-go/v2/instrumentation/mcp"
	"github.com/DataDog/dd-trace-go/v2/llmobs"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type hooks struct {
	spanCache *sync.Map
}

// appendTracingHooks appends Datadog tracing hooks to an existing server.Hooks object.
func appendTracingHooks(hooks *server.Hooks) {
	tracingHooks := newHooks()
	hooks.AddBeforeInitialize(tracingHooks.onBeforeInitialize)
	hooks.AddAfterInitialize(tracingHooks.onAfterInitialize)
	hooks.AddOnError(tracingHooks.onError)
}

// redactToolOutputKey is the context key set by redactToolOutputMiddleware
// to flag the request's tool output as needing redaction in the LLMObs span.
type redactToolOutputKey struct{}

// redactToolOutputMiddleware is registered when TracingConfig.RedactToolOutput
// is true; it tags the request context so toolHandlerMiddleware redacts the
// tool output it later annotates onto the LLMObs span.
var redactToolOutputMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return next(context.WithValue(ctx, redactToolOutputKey{}, struct{}{}), request)
	}
}

var toolHandlerMiddleware = func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		startOpts := []llmobs.StartSpanOption{
			llmobs.WithIntegration(string(instrumentation.PackageMark3LabsMCPGo)),
		}
		if session := server.ClientSessionFromContext(ctx); session != nil {
			startOpts = append(startOpts, llmobs.WithSessionID(session.SessionID()))
		}
		toolSpan, ctx := llmobs.StartToolSpan(ctx, request.Params.Name, startOpts...)

		var result *mcp.CallToolResult
		var err error

		defer func() {
			inputJSON, marshalErr := json.Marshal(request)
			if marshalErr != nil {
				instr.Logger().Warn("mcp-go: failed to marshal tool request: %v", marshalErr)
			}
			var outputText string
			if result != nil {
				if _, redact := ctx.Value(redactToolOutputKey{}).(struct{}); redact {
					outputText = "[REDACTED]"
				} else {
					resultJSON, marshalErr := json.Marshal(result)
					if marshalErr != nil {
						instr.Logger().Warn("mcp-go: failed to marshal tool result: %v", marshalErr)
					}
					outputText = string(resultJSON)
				}
			}

			toolSpan.Annotate(llmobs.WithAnnotatedTags(map[string]string{
				instrmcp.MCPToolTag:     request.Params.Name,
				instrmcp.MCPToolKindTag: "server",
				instrmcp.MCPMethodTag:   request.Method,
			}))
			tagWithSessionID(ctx, toolSpan)
			toolSpan.AnnotateTextIO(string(inputJSON), outputText)

			// There are two ways a tool can express an error:
			// 1. It can return a Go error.
			// 2. It can return IsError: true as part of the tool result.
			if err != nil {
				toolSpan.Finish(llmobs.WithError(err))
			} else if result != nil && result.IsError {
				toolSpan.Finish(llmobs.WithError(errors.New("tool resulted in an error")))
			} else {
				toolSpan.Finish()
			}
		}()

		result, err = next(ctx, request)

		return result, err
	}
}

func newHooks() *hooks {
	return &hooks{
		spanCache: &sync.Map{},
	}
}

func (h *hooks) onBeforeInitialize(ctx context.Context, id any, request *mcp.InitializeRequest) {
	taskSpan, _ := llmobs.StartTaskSpan(ctx, "mcp.initialize", llmobs.WithIntegration(string(instrumentation.PackageMark3LabsMCPGo)))

	clientName := request.Params.ClientInfo.Name
	clientVersion := request.Params.ClientInfo.Version
	taskSpan.Annotate(llmobs.WithAnnotatedTags(map[string]string{
		instrmcp.MCPClientNameTag:    clientName,
		instrmcp.MCPClientVersionTag: clientName + "_" + clientVersion,
		instrmcp.MCPMethodTag:        request.Method,
	}))

	h.spanCache.Store(id, taskSpan)
	tagWithSessionID(ctx, taskSpan)
}

func (h *hooks) onAfterInitialize(ctx context.Context, id any, request *mcp.InitializeRequest, result *mcp.InitializeResult) {
	finishSpanWithIO(h, id, request, result)
}

func (h *hooks) onError(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
	if method != mcp.MethodInitialize {
		return
	}
	value, ok := h.spanCache.LoadAndDelete(id)
	if !ok {
		return
	}

	span, ok := value.(*llmobs.TaskSpan)
	if !ok {
		return
	}

	defer span.Finish(llmobs.WithError(err))

	inputJSON, marshalErr := json.Marshal(message)
	if marshalErr != nil {
		instr.Logger().Warn("mcp-go: failed to marshal error message: %v", marshalErr)
	}
	span.AnnotateTextIO(string(inputJSON), err.Error())

}

func tagWithSessionID(ctx context.Context, span llmobs.Span) {
	session := server.ClientSessionFromContext(ctx)
	if session != nil {
		sessionID := session.SessionID()
		span.Annotate(llmobs.WithAnnotatedTags(map[string]string{instrmcp.MCPSessionIDTag: sessionID}))
	}
}

func finishSpanWithIO[Req any, Res any](h *hooks, id any, request Req, result Res) {
	value, ok := h.spanCache.LoadAndDelete(id)
	if !ok {
		return
	}
	span, ok := value.(*llmobs.TaskSpan)
	if !ok {
		return
	}

	defer span.Finish()

	inputJSON, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		instr.Logger().Warn("mcp-go: failed to marshal request: %v", marshalErr)
	}
	resultJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		instr.Logger().Warn("mcp-go: failed to marshal result: %v", marshalErr)
	}

	span.AnnotateTextIO(string(inputJSON), string(resultJSON))
}
