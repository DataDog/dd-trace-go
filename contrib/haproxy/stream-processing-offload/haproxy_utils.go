// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"

	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/message"
)

func getStringValue(msg *message.Message, key string) string {
	if val, exists := msg.KV.Get(key); exists {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getIntValue(msg *message.Message, key string) int {
	if val, exists := msg.KV.Get(key); exists {
		if i, ok := val.(int); ok {
			return i
		}
		if i64, ok := val.(int64); ok {
			return int(i64)
		}
	}
	return 0
}

func getBytesArrayValue(msg *message.Message, key string) []byte {
	if val, exists := msg.KV.Get(key); exists {
		if bytes, ok := val.([]byte); ok {
			return bytes
		}
	}
	return nil
}

func getBoolValue(msg *message.Message, key string) bool {
	if val, exists := msg.KV.Get(key); exists {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func getIPValue(msg *message.Message, key string) net.IP {
	if val, exists := msg.KV.Get(key); exists {
		if ip, ok := val.(net.IP); ok {
			return ip
		}
	}
	return nil
}

// spanIDFromMessage extracts the `span_id` from the agent message to use as the key for the request state cache.
func spanIDFromMessage(msg *message.Message) (uint64, error) {
	spanIdStr := getStringValue(msg, "span_id")

	if spanIdStr == "" {
		return 0, fmt.Errorf("span_id not found in message")
	}

	spanId, err := strconv.ParseUint(spanIdStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse span_id '%s': %v", spanIdStr, err)
	}
	if spanId == 0 {
		return 0, fmt.Errorf("span_id is 0")
	}

	return spanId, nil
}

// continueActionFunc sets HeadersResponseData data into the request variables answering a Request Headers message
func continueActionFunc(ctx context.Context, options proxy.ContinueActionOptions) error {
	requestContextData, _ := ctx.Value(haproxyRequestContextKey{}).(*haproxyRequestContextData)
	if requestContextData == nil {
		return fmt.Errorf("no haproxy request data found in context")
	}

	if requestContextData.req == nil || requestContextData.req.Actions == nil {
		return fmt.Errorf("the haproxy context data have not been correctly initialized")
	}

	// Only set the span id from a request headers message
	if options.MessageType == proxy.MessageTypeRequestHeaders {
		s, ok := tracer.SpanFromContext(ctx)
		if !ok {
			return fmt.Errorf("failed to retreive the span from the context of the request")
		}

		timeout := getStringValue(requestContextData.msg, "timeout")
		requestContextData.timeout = timeout

		spanId := s.Context().SpanID()
		spanIdStr := strconv.FormatUint(spanId, 10)
		requestContextData.req.Actions.SetVar(action.ScopeTransaction, "span_id", spanIdStr)

		injectTracingHeaders(options.HeaderMutations, &requestContextData.req.Actions)
	}

	if options.Body {
		requestContextData.req.Actions.SetVar(action.ScopeTransaction, "request_body", true)
	}

	return nil
}

const headerCount = 5

// haproxyTracingHeaderActions defines the names of the actions to set tracing headers for HAProxy.
// These action names are used inside the HAProxy configuration to correctly set the tracing headers.
var haproxyTracingHeaderActions = [headerCount]string{
	"tracing_x_datadog_trace_id",
	"tracing_x_datadog_parent_id",
	"tracing_x_datadog_origin",
	"tracing_x_datadog_sampling_priority",
	"tracing_x_datadog_tags",
}

// datadogTracingHeaders defines the names of tracing headers supported with the Datadog tracing format.
var datadogTracingHeaders = [headerCount]string{
	tracer.DefaultTraceIDHeader,
	tracer.DefaultParentIDHeader,
	"x-datadog-origin",
	tracer.DefaultPriorityHeader,
	"x-datadog-tags",
}

// injectTracingHeaders injects tracing headers when present. Supporting only the Datadog tracing format.
// https://docs.datadoghq.com/tracing/trace_collection/trace_context_propagation/#datadog-format
func injectTracingHeaders(headerMutations map[string][]string, actions *action.Actions) {
	if len(headerMutations) == 0 {
		return
	}

	for i := range haproxyTracingHeaderActions {
		mutationHeader := http.CanonicalHeaderKey(datadogTracingHeaders[i])
		if v, ok := headerMutations[mutationHeader]; ok {
			actions.SetVar(action.ScopeTransaction, haproxyTracingHeaderActions[i], strings.TrimSpace(strings.Join(v, ",")))
		}
	}
}

// blockActionFunc sets blocked data into the request variables when the request is blocked
func blockActionFunc(ctx context.Context, data proxy.BlockActionOptions) error {
	requestContext, _ := ctx.Value(haproxyRequestContextKey{}).(*haproxyRequestContextData)
	if requestContext == nil {
		return fmt.Errorf("no haproxy request data found in context")
	}

	if requestContext.req == nil || requestContext.req.Actions == nil {
		return fmt.Errorf("the haproxy context data have not been correctly initialized")
	}

	requestContext.req.Actions.SetVar(action.ScopeTransaction, "blocked", true)
	requestContext.req.Actions.SetVar(action.ScopeTransaction, "headers", convertHeadersToString(data.Headers))
	requestContext.req.Actions.SetVar(action.ScopeTransaction, "body", data.Body)
	requestContext.req.Actions.SetVar(action.ScopeTransaction, "status_code", data.StatusCode)

	return nil
}

// convertHeadersToString converts HTTP headers to a string format with `Header: Value` pairs separated by newlines.
// These headers will then be parsed by a lua script loaded in the HAProxy configuration.
func convertHeadersToString(headers http.Header) string {
	var sb strings.Builder
	for key, values := range headers {
		sb.WriteString(key)
		sb.WriteString(": ")
		sb.WriteString(strings.Join(values, ","))
		sb.WriteString("\n")
	}
	return sb.String()
}
