// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"io"
	"net/http"
	"path"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

var _ io.Closer = (*requestState)(nil)

const (
	componentNameEnvoy               = "envoyproxy/go-control-plane"
	componentNameGCPServiceExtension = "gcp-service-extension"
)

// requestState manages the state of a single request through its lifecycle
type requestState struct {
	Ctx         context.Context
	Span        *tracer.Span
	AfterHandle func()

	// HTTP components
	WrappedResponseWriter http.ResponseWriter
	FakeResponseWriter    *fakeResponseWriter

	// Body processing
	RequestBuffer  *bodyBuffer
	ResponseBuffer *bodyBuffer

	// Processing state
	Ongoing              bool
	Blocked              bool
	AwaitingRequestBody  bool
	AwaitingResponseBody bool
}

// newRequestState creates a new request state
func newRequestState(request *http.Request, bodyLimit int, isGCPServiceExtension bool) (requestState, bool) {
	componentName := componentNameEnvoy
	if isGCPServiceExtension {
		componentName = componentNameGCPServiceExtension
	}

	fakeResponseWriter := newFakeResponseWriter()
	wrappedResponseWriter, spanRequest, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
		Framework: "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3",
		Resource:  request.Method + " " + path.Clean(request.URL.Path),
		SpanOpts: []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, componentName),
		},
	}, fakeResponseWriter, request)

	span, ok := tracer.SpanFromContext(spanRequest.Context())
	if !ok {
		return requestState{}, false
	}

	var requestBuffer *bodyBuffer
	if bodyLimit > 0 {
		requestBuffer = newBodyBuffer(bodyLimit)
	}

	var responseBuffer *bodyBuffer
	if bodyLimit > 0 {
		responseBuffer = newBodyBuffer(bodyLimit)
	}

	return requestState{
		Ctx:                   spanRequest.Context(),
		Span:                  span,
		AfterHandle:           afterHandle,
		FakeResponseWriter:    fakeResponseWriter,
		WrappedResponseWriter: wrappedResponseWriter,
		RequestBuffer:         requestBuffer,
		ResponseBuffer:        responseBuffer,
		Ongoing:               true,
	}, blocked
}

// PropagationHeaders creates header mutations for trace propagation
func (rs *requestState) PropagationHeaders() ([]*envoycore.HeaderValueOption, error) {
	newHeaders := make(http.Header)
	if err := tracer.Inject(rs.Span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, err
	}

	if len(newHeaders) > 0 {
		instr.Logger().Debug("external_processing: injecting propagation headers: %v\n", newHeaders)
	}

	return convertHeadersToEnvoy(newHeaders), nil
}

// SetBlocked marks the request as blocked and completes it.
func (rs *requestState) SetBlocked() {
	rs.Blocked = true
	rs.Close()
}

// Close finalizes the request processing.
func (rs *requestState) Close() error {
	if rs.AfterHandle != nil {
		// Avoid Complete recursion by clearing afterHandle before calling it
		afterHandle := rs.AfterHandle
		rs.AfterHandle = nil
		afterHandle()
	}
	rs.Ongoing = false
	return nil
}
