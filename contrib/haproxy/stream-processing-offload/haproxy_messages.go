// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"fmt"
	"strconv"

	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/proxy"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var _ proxy.RequestHeaders = (*messageRequestHeaders)(nil)
var _ proxy.ResponseHeaders = (*responseHeadersHAProxy)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)

type messageRequestHeaders struct {
	req     *request.Request
	msg     *message.Message
	hasBody bool
}

func (m messageRequestHeaders) ExtractRequest(_ context.Context) (proxy.PseudoRequest, error) {
	headers, err := parseHAProxyReqHdrsBin(getBytesArrayValue(m.msg, "headers"))
	if err != nil {
		return proxy.PseudoRequest{}, err
	}

	authority := headers.Get("Host")
	method := getStringValue(m.msg, "method")
	path := getStringValue(m.msg, "path")
	https := getBoolValue(m.msg, "https")
	remoteIp := getIPValue(m.msg, "ip")
	remotePort := strconv.Itoa(getIntValue(m.msg, "ip_port"))

	if authority == "" || method == "" || path == "" || remoteIp == nil {
		return proxy.PseudoRequest{}, fmt.Errorf("missing required values in the http request SPOE message")
	}

	scheme := "http"
	if https {
		scheme = "https"
	}

	// Define if a body is present, based on Content-Length header
	contentLength := headers.Get("Content-Length")
	if contentLength != "" {
		length, err := strconv.Atoi(contentLength)
		if err != nil {
			return proxy.PseudoRequest{}, fmt.Errorf("invalid Content-Length header: %v", err)
		}
		m.hasBody = length > 0
	}

	remoteAddr := remoteIp.String() + ":" + remotePort

	return proxy.PseudoRequest{
		Method:     method,
		Authority:  authority,
		Path:       path,
		Scheme:     scheme,
		Headers:    headers,
		RemoteAddr: remoteAddr,
	}, nil
}

func (m messageRequestHeaders) GetEndOfStream() bool {
	return !m.hasBody
}

func (m messageRequestHeaders) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestHeaders
}

const componentNameHAProxySPOA = "haproxy-spoa"

func (m messageRequestHeaders) SpanOptions(_ context.Context) []tracer.StartSpanOption {
	return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameHAProxySPOA)}
}

type responseHeadersHAProxy struct {
	msg     *message.Message
	hasBody bool
}

func (m responseHeadersHAProxy) ExtractResponse() (proxy.PseudoResponse, error) {
	headers, err := parseHAProxyReqHdrsBin(getBytesArrayValue(m.msg, "headers"))
	if err != nil {
		return proxy.PseudoResponse{}, err
	}

	status := getIntValue(m.msg, "status_code")

	// Set has body based on Content-Length header
	contentLength := headers.Get("Content-Length")
	if contentLength != "" {
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

func (m responseHeadersHAProxy) GetEndOfStream() bool {
	return !m.hasBody
}

func (m responseHeadersHAProxy) MessageType() proxy.MessageType {
	return proxy.MessageTypeResponseHeaders
}

type messageBody struct {
	msg *message.Message
	m   proxy.MessageType
}

func (m messageBody) GetEndOfStream() bool {
	return true
}

func (m messageBody) GetBody() []byte {
	return getBytesArrayValue(m.msg, "body")
}

func (m messageBody) MessageType() proxy.MessageType {
	return m.m
}
