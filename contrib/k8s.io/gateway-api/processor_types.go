// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi

import (
	"context"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
)

var _ proxy.RequestHeaders = (*requestHeader)(nil)

type requestHeader struct {
	*http.Request
	spanOptions []tracer.StartSpanOption
}

func (r requestHeader) GetEndOfStream() bool {
	return r.Request.Body == http.NoBody || r.Request.ContentLength == 0 || r.Request.Body == nil
}

func (r requestHeader) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestHeaders
}

func (r requestHeader) ExtractRequest(_ context.Context) (proxy.PseudoRequest, error) {
	return proxy.PseudoRequest{
		Method:     r.Method,
		Headers:    r.Header,
		Scheme:     r.URL.Scheme,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Authority:  r.URL.Host,
	}, nil
}

func (r requestHeader) SpanOptions(_ context.Context) []tracer.StartSpanOption {
	return r.spanOptions
}

var _ proxy.HTTPBody = (*requestBody)(nil)

type requestBody struct {
	body []byte
}

func (r requestBody) GetEndOfStream() bool {
	return true
}

func (r requestBody) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestBody
}

func (r requestBody) GetBody() []byte {
	return r.body
}
