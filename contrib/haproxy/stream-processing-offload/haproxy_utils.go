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

// haproxyMessage wraps the SPOE message and provides typed accessors
type haproxyMessage struct {
	*message.Message
}

// newHaproxyMessage creates a new haproxyMessage wrapper of message.Message
func newHaproxyMessage(msg *message.Message) *haproxyMessage {
	return &haproxyMessage{Message: msg}
}

// String returns the string value for the given key, or returns an empty string if it's missing or not a string
func (m *haproxyMessage) String(key string) string {
	if val, exists := m.KV.Get(key); exists {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// Int returns the int value for the given key or 0 if missing
func (m *haproxyMessage) Int(key string) int {
	if val, exists := m.KV.Get(key); exists {
		if i, ok := val.(int); ok {
			return i
		}
		if i64, ok := val.(int64); ok {
			return int(i64)
		}
	}
	return 0
}

// Bytes returns the []byte value for the given key or nil if missing
func (m *haproxyMessage) Bytes(key string) []byte {
	if val, exists := m.KV.Get(key); exists {
		if b, ok := val.([]byte); ok {
			return b
		}
	}
	return nil
}

// Bool returns the bool value for the given key or false if missing
func (m *haproxyMessage) Bool(key string) bool {
	if val, exists := m.KV.Get(key); exists {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

// IP returns the net.IP value for the given key or nil if missing
func (m *haproxyMessage) IP(key string) net.IP {
	if val, exists := m.KV.Get(key); exists {
		if ip, ok := val.(net.IP); ok {
			return ip
		}
	}
	return nil
}

// SpanID extracts the `span_id` from the message and returns it as uint64.
func (m *haproxyMessage) SpanID() (uint64, error) {
	spanIdStr := m.String(VarSpanId)
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
	requestContextData, found := ctx.Value(haproxyRequestContextKey{}).(*haproxyRequestContextData)
	if !found {
		return fmt.Errorf("no haproxy request data found in context")
	}

	if requestContextData.req == nil || requestContextData.req.Actions == nil {
		return fmt.Errorf("the haproxy context data have not been correctly initialized")
	}

	// Only set the span id from a request headers message
	if options.HeaderMutations != nil {
		s, ok := tracer.SpanFromContext(ctx)
		if !ok {
			return fmt.Errorf("failed to retreive the span from the context of the request")
		}

		timeout := requestContextData.msg.String(VarTimeout)
		requestContextData.timeout = timeout

		spanId := s.Context().SpanID()
		spanIdStr := strconv.FormatUint(spanId, 10)
		requestContextData.req.Actions.SetVar(action.ScopeTransaction, VarSpanId, spanIdStr)

		injectTracingHeaders(options.HeaderMutations, &requestContextData.req.Actions)
	}

	if options.Body {
		requestContextData.req.Actions.SetVar(action.ScopeTransaction, VarRequestBody, true)
	}

	return nil
}

const headerCount = 5

// haproxyTracingHeaderActions defines the names of the actions to set tracing headers for HAProxy.
// These action names are used inside the HAProxy configuration to correctly set the tracing headers.
var haproxyTracingHeaderActions = [headerCount]string{
	VarTracingHeaderTraceId,
	VarTracingHeaderParentId,
	VarTracingHeaderOrigin,
	VarTracingHeaderSamplingPriority,
	VarTracingHeaderTags,
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
	requestContext, found := ctx.Value(haproxyRequestContextKey{}).(*haproxyRequestContextData)
	if !found {
		return fmt.Errorf("no haproxy request data found in context")
	}

	if requestContext.req == nil || requestContext.req.Actions == nil {
		return fmt.Errorf("the haproxy context data have not been correctly initialized")
	}

	requestContext.req.Actions.SetVar(action.ScopeTransaction, VarBlocked, true)
	requestContext.req.Actions.SetVar(action.ScopeTransaction, VarHeaders, convertHeadersToString(data.Headers))
	requestContext.req.Actions.SetVar(action.ScopeTransaction, VarBody, data.Body)
	requestContext.req.Actions.SetVar(action.ScopeTransaction, VarStatus, data.StatusCode)

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
