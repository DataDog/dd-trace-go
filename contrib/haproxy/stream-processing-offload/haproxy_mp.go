// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"net/http"

	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
)

type requestHeadersHAProxy struct {
	req *request.Request
	msg *message.Message
}

func (a *requestHeadersHAProxy) NewRequest(ctx context.Context) (*http.Request, error) {
	headers, err := parseHAProxyReqHdrsBin(getBytesArrayValue(a.msg, "headers"))
	if err != nil {
		return nil, err
	}

	method := getStringValue(a.msg, "method")
	path := getStringValue(a.msg, "path")
	https := getBoolValue(a.msg, "https")

	var tlsState *tls.ConnectionState
	scheme := "http"
	if https {
		tlsState = &tls.ConnectionState{}
		scheme = "https"
	}

	authority := headers.Get("Host")
	if authority == "" {
		return nil, fmt.Errorf("no Host header")
	}

	return message_processor.NewRequest(ctx,
		scheme,
		authority,
		path,
		method,
		headers,
		"18.8.8.8",
		tlsState)
}

func (a *requestHeadersHAProxy) EndOfStream() bool {
	return true
}

func (a *requestHeadersHAProxy) Component(_ context.Context) string {
	return "haproxy-spoe"
}

func (a *requestHeadersHAProxy) Framework() string {
	return "to-define"
}

type requestBodyHAProxy struct {
	msg *message.Message
}

func (a *requestBodyHAProxy) Body() []byte {
	return getBytesArrayValue(a.msg, "body")
}

func (a *requestBodyHAProxy) EndOfStream() bool {
	return true
}

type responseHeadersHAProxy struct {
	msg *message.Message
}

func (a *responseHeadersHAProxy) InitResponseWriter(w http.ResponseWriter) error {
	headers := make(http.Header) // todo
	status := getIntValue(a.msg, "status_code")

	for k, v := range headers {
		w.Header()[k] = v
	}

	w.WriteHeader(status)
	return nil
}

func (a *responseHeadersHAProxy) EndOfStream() bool {
	return true
}

type responseBodyHAProxy struct {
	msg *message.Message
}

func (a *responseBodyHAProxy) Body() []byte {
	return getBytesArrayValue(a.msg, "body")
}

func (a *responseBodyHAProxy) EndOfStream() bool {
	return true
}
