// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
	"github.com/negasus/haproxy-spoe-go/request"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// The following constants define the variable names used for communication between
// the Go SPOA agent and HAProxy, based on the SPOE configuration.
// IMPORTANT: If you change any of these values, you MUST also update the corresponding
// variable names in the HAProxy configuration to match, or the integration will break.
const (
	VarIp          = "ip"           // The remote IP address of the client as net.IP
	VarIpPort      = "ip_port"      // The remote port of the client as an int.
	VarMethod      = "method"       // The HTTP method of the request as a string.
	VarPath        = "path"         // The path of the request as a string.
	VarHeaders     = "headers"      // The binary headers of the request as a []byte.
	VarHttps       = "https"        // The request is HTTPS as a bool.
	VarTimeout     = "timeout"      // The timeout duration of the request as a string.
	VarStatus      = "status"       // The status code of the response as an int.
	VarBody        = "body"         // The body of the request as a []byte.
	VarSpanId      = "span_id"      // The span ID of the request as a string.
	VarBlocked     = "blocked"      // The request is blocked as a bool.
	VarRequestBody = "request_body" // The body of the request or response is requested by the SPOA, as a bool.

	VarTracingHeaderTraceId          = "tracing_x_datadog_trace_id"          // The Datadog trace ID header of the request as a string.
	VarTracingHeaderParentId         = "tracing_x_datadog_parent_id"         // The Datadog parent ID header of the request as a string.
	VarTracingHeaderOrigin           = "tracing_x_datadog_origin"            // The Datadog origin header of the request as a string.
	VarTracingHeaderSamplingPriority = "tracing_x_datadog_sampling_priority" // The Datadog sampling priority header of the request as a string.
	VarTracingHeaderTags             = "tracing_x_datadog_tags"              // The Datadog tags header of the request as a string.
)

var _ proxy.RequestHeaders = (*messageRequestHeaders)(nil)
var _ proxy.ResponseHeaders = (*responseHeadersHAProxy)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)

type messageRequestHeaders struct {
	req     *request.Request
	msg     *haproxyMessage
	hasBody bool
}

func (m *messageRequestHeaders) ExtractRequest(_ context.Context) (proxy.PseudoRequest, error) {
	headers, err := parseHAProxyReqHdrsBin(m.msg.Bytes(VarHeaders))
	if err != nil {
		return proxy.PseudoRequest{}, err
	}

	authority := headers.Get("Host")
	method := m.msg.String(VarMethod)
	path := m.msg.String(VarPath)
	https := m.msg.Bool(VarHttps)

	if authority == "" || method == "" || path == "" {
		return proxy.PseudoRequest{}, fmt.Errorf("missing required values in the http request SPOE message")
	}

	scheme := "http"
	if https {
		scheme = "https"
	}

	m.hasBody = true

	// Refine body presence if Content-Length is set
	if contentLength := headers.Get("Content-Length"); contentLength != "" {
		length, err := strconv.Atoi(contentLength)
		if err != nil {
			return proxy.PseudoRequest{}, fmt.Errorf("invalid Content-Length header: %v", err)
		}
		m.hasBody = length > 0
	}

	var remoteAddr string
	remoteIp := m.msg.IP(VarIp)
	if remoteIp != nil {
		remotePort := strconv.Itoa(m.msg.Int(VarIpPort))
		remoteAddr = remoteIp.String() + ":" + remotePort
	}

	return proxy.PseudoRequest{
		Method:     method,
		Authority:  authority,
		Path:       path,
		Scheme:     scheme,
		Headers:    headers,
		RemoteAddr: remoteAddr,
	}, nil
}

func (m *messageRequestHeaders) GetEndOfStream() bool {
	return !m.hasBody
}

func (m *messageRequestHeaders) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestHeaders
}

const componentNameHAProxySPOA = "haproxy-spoa"

func (m *messageRequestHeaders) SpanOptions(_ context.Context) []tracer.StartSpanOption {
	return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameHAProxySPOA)}
}

type responseHeadersHAProxy struct {
	msg     *haproxyMessage
	hasBody bool
}

func (m *responseHeadersHAProxy) ExtractResponse() (proxy.PseudoResponse, error) {
	headers, err := parseHAProxyReqHdrsBin(m.msg.Bytes(VarHeaders))
	if err != nil {
		return proxy.PseudoResponse{}, err
	}

	status := m.msg.Int(VarStatus)

	m.hasBody = true

	// Refine body presence if Content-Length is set
	if contentLength := headers.Get("Content-Length"); contentLength != "" {
		length, err := strconv.Atoi(contentLength)
		if err != nil {
			return proxy.PseudoResponse{}, fmt.Errorf("invalid Content-Length header: %v", err)
		}
		m.hasBody = length > 0
	}

	return proxy.PseudoResponse{
		StatusCode: status,
		Headers:    headers,
	}, nil
}

func (m *responseHeadersHAProxy) GetEndOfStream() bool {
	return !m.hasBody
}

func (m *responseHeadersHAProxy) MessageType() proxy.MessageType {
	return proxy.MessageTypeResponseHeaders
}

type messageBody struct {
	msg *haproxyMessage
	m   proxy.MessageType
}

func (m messageBody) GetEndOfStream() bool {
	return true
}

func (m messageBody) GetBody() []byte {
	return m.msg.Bytes(VarBody)
}

func (m messageBody) MessageType() proxy.MessageType {
	return m.m
}
