// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package envoy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/metadata"
)

// checkPseudoRequestHeaders Verify the required HTTP2 headers are present
// Some mandatory headers need to be set. It can happen when it wasn't a real HTTP2 request sent by Envoy,
func checkPseudoRequestHeaders(headers map[string]string) error {
	for _, header := range []string{":authority", ":scheme", ":path", ":method"} {
		if _, ok := headers[header]; !ok {
			return fmt.Errorf("missing required headers: %q", header)
		}
	}

	return nil
}

// checkPseudoResponseHeaders Verify the required HTTP2 headers are present
// Some mandatory headers need to be set. It can happen when it wasn't a real HTTP2 request sent by Envoy,
func checkPseudoResponseHeaders(headers map[string]string) error {
	if _, ok := headers[":status"]; !ok {
		return fmt.Errorf("missing required ':status' headers")
	}

	return nil
}

func getRemoteAddr(md metadata.MD) string {
	xfwd := md.Get("x-forwarded-for")
	length := len(xfwd)
	if length == 0 {
		return ""
	}

	// Get the first right value of x-forwarded-for headers
	// The rightmost IP address is the one that will be used as the remote client IP
	// https://datadoghq.atlassian.net/wiki/spaces/TS/pages/2766733526/Sensitive+IP+information#Where-does-the-value-of-the-http.client_ip-tag-come-from%3F
	return xfwd[length-1]
}

// partitionPeusdoHeaders Separate normal headers of the initial request made by the client and the pseudo headers of HTTP/2
// - Format the headers to be used by the tracer as a map[string][]string
// - Set headers keys to be canonical
func partitionPeusdoHeaders(receivedHeaders []*corev3.HeaderValue) (map[string][]string, map[string]string) {
	headers := make(map[string][]string, len(receivedHeaders)-4)
	pseudoHeaders := make(map[string]string, 4)
	for _, v := range receivedHeaders {
		key := v.GetKey()
		if key == "" {
			continue
		}
		if key[0] == ':' {
			pseudoHeaders[key] = string(v.GetRawValue())
			continue
		}

		headers[http.CanonicalHeaderKey(key)] = []string{string(v.GetRawValue())}
	}
	return headers, pseudoHeaders
}

func NewFakeResponseWriterFromExtProc(w http.ResponseWriter, res *extproc.ProcessingRequest_ResponseHeaders) error {
	headers, pseudoHeaders := partitionPeusdoHeaders(res.ResponseHeaders.GetHeaders().GetHeaders())

	if err := checkPseudoResponseHeaders(pseudoHeaders); err != nil {
		return err
	}

	status, err := strconv.Atoi(pseudoHeaders[":status"])
	if err != nil {
		return fmt.Errorf("error parsing status code %q: %w", pseudoHeaders[":status"], err)
	}

	for k, v := range headers {
		w.Header().Set(k, strings.Join(v, ","))
	}

	w.WriteHeader(status)
	return nil
}

// NewRequestFromExtProc creates a new http.Request from an ext_proc RequestHeaders message
func NewRequestFromExtProc(ctx context.Context, req *extproc.ProcessingRequest_RequestHeaders) (*http.Request, error) {
	headers, pseudoHeaders := partitionPeusdoHeaders(req.RequestHeaders.GetHeaders().GetHeaders())
	if err := checkPseudoRequestHeaders(pseudoHeaders); err != nil {
		return nil, err
	}

	parsedURL, err := url.Parse(fmt.Sprintf("%s://%s%s", pseudoHeaders[":scheme"], pseudoHeaders[":authority"], pseudoHeaders[":path"]))
	if err != nil {
		return nil, fmt.Errorf(
			"error building envoy URI from scheme %q, from host %q and from path %q: %w",
			pseudoHeaders[":scheme"],
			pseudoHeaders[":host"],
			pseudoHeaders[":path"],
			err)
	}

	var remoteAddr string
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		remoteAddr = getRemoteAddr(md)
	}

	var tlsState *tls.ConnectionState
	if pseudoHeaders[":scheme"] == "https" {
		tlsState = &tls.ConnectionState{}
	}

	headers["Host"] = append(headers["Host"], pseudoHeaders[":authority"])

	return (&http.Request{
		Method:     pseudoHeaders[":method"],
		Host:       pseudoHeaders[":authority"],
		RequestURI: pseudoHeaders[":path"],
		URL:        parsedURL,
		Header:     headers,
		RemoteAddr: remoteAddr,
		TLS:        tlsState,
	}).WithContext(ctx), nil
}

type FakeResponseWriter struct {
	mu      sync.Mutex
	status  int
	body    []byte
	headers http.Header
}

// Reset resets the FakeResponseWriter to its initial state
func (w *FakeResponseWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = 0
	w.body = nil
	w.headers = make(http.Header)
}

// Status is not in the [http.ResponseWriter] interface, but it is cast into it by the tracing code
func (w *FakeResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *FakeResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}

func (w *FakeResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.headers
}

func (w *FakeResponseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.body = append(w.body, b...)
	return len(b), nil
}

var _ http.ResponseWriter = &FakeResponseWriter{}

// NewFakeResponseWriter creates a new FakeResponseWriter that can be used to store the response a [http.Handler] made
func NewFakeResponseWriter() *FakeResponseWriter {
	return &FakeResponseWriter{
		headers: make(http.Header),
	}
}
