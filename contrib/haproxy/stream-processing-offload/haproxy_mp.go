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
	"strconv"

	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
)

type requestHeadersHAProxy struct {
	req     *request.Request
	msg     *message.Message
	hasBody bool
}

func (a *requestHeadersHAProxy) NewRequest(ctx context.Context) (*http.Request, error) {
	headers, err := parseHAProxyReqHdrsBin(getBytesArrayValue(a.msg, "headers"))
	if err != nil {
		return nil, err
	}

	method := getStringValue(a.msg, "method")
	path := getStringValue(a.msg, "path")
	https := getBoolValue(a.msg, "https")
	remoteIp := getIPValue(a.msg, "ip")
	remotePort := strconv.Itoa(getIntValue(a.msg, "ip_port"))

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

	// Define if a body is present based on Content-Length header
	contentLength := headers.Get("Content-Length")
	if contentLength != "" {
		length, err := strconv.Atoi(contentLength)
		if err != nil {
			return nil, fmt.Errorf("invalid Content-Length header: %v", err)
		}
		a.hasBody = length > 0
	}

	return message_processor.NewRequest(ctx,
		scheme,
		authority,
		path,
		method,
		headers,
		remoteIp.String()+":"+remotePort,
		tlsState)
}

func (a *requestHeadersHAProxy) EndOfStream() bool {
	return !a.hasBody
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
	msg     *message.Message
	hasBody bool
}

func (a *responseHeadersHAProxy) InitResponseWriter(w http.ResponseWriter) error {
	headers, err := parseHAProxyReqHdrsBin(getBytesArrayValue(a.msg, "headers"))
	if err != nil {
		return err
	}

	status := getIntValue(a.msg, "status_code")

	for k, v := range headers {
		w.Header()[k] = v
	}

	// Set has body based on Content-Length header
	contentLength := headers.Get("Content-Length")
	if contentLength != "" {
		length, err := strconv.Atoi(contentLength)
		if err != nil {
			return fmt.Errorf("invalid Content-Length header: %v", err)
		}
		a.hasBody = length > 0
	}

	w.WriteHeader(status)
	return nil
}

func (a *responseHeadersHAProxy) EndOfStream() bool {
	return !a.hasBody
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
