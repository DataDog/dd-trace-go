// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package message_processor

import (
	"context"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"io"
	"net/http"
	"path"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

var _ io.Closer = (*RequestState)(nil)

// RequestState manages the state of a single request through its lifecycle
type RequestState struct {
	ctx         context.Context
	Span        *tracer.Span
	afterHandle func()

	instr *instrumentation.Instrumentation

	// HTTP components
	wrappedResponseWriter http.ResponseWriter
	fakeResponseWriter    *fakeResponseWriter

	// Body processing
	requestBuffer  *bodyBuffer
	responseBuffer *bodyBuffer

	// Processing state
	blocked              bool
	Ongoing              bool
	AwaitingRequestBody  bool
	AwaitingResponseBody bool
}

// newRequestState creates a new request state
func newRequestState(request *http.Request, instr *instrumentation.Instrumentation, bodyLimit int, componentName string, framework string) (*RequestState, bool) {
	fakeResponseWriter := newFakeResponseWriter()
	wrappedResponseWriter, spanRequest, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
		Framework: framework,
		Resource:  request.Method + " " + path.Clean(request.URL.Path),
		SpanOpts: []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, componentName),
		},
	}, fakeResponseWriter, request)

	span, ok := tracer.SpanFromContext(spanRequest.Context())
	if !ok {
		return nil, false
	}

	var requestBuffer *bodyBuffer
	if bodyLimit > 0 {
		requestBuffer = newBodyBuffer(bodyLimit)
	}

	var responseBuffer *bodyBuffer
	if bodyLimit > 0 {
		responseBuffer = newBodyBuffer(bodyLimit)
	}

	return &RequestState{
		ctx:                   spanRequest.Context(),
		Span:                  span,
		afterHandle:           afterHandle,
		instr:                 instr,
		fakeResponseWriter:    fakeResponseWriter,
		wrappedResponseWriter: wrappedResponseWriter,
		requestBuffer:         requestBuffer,
		responseBuffer:        responseBuffer,
		Ongoing:               true,
	}, blocked
}

// PropagationHeaders creates header mutations for trace propagation
func (rs *RequestState) PropagationHeaders() (http.Header, error) {
	newHeaders := make(http.Header)
	if err := tracer.Inject(rs.Span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, err
	}

	if len(newHeaders) > 0 {
		rs.instr.Logger().Debug("message_processor: injecting propagation headers: %v\n", newHeaders)
	}

	return newHeaders, nil
}

// SetBlocked marks the request as blocked and completes it.
func (rs *RequestState) SetBlocked() {
	rs.blocked = true
	rs.Close()
}

// Close finalizes the request processing.
func (rs *RequestState) Close() error {
	if rs.afterHandle != nil {
		// Avoid Complete recursion by clearing afterHandle before calling it
		afterHandle := rs.afterHandle
		rs.afterHandle = nil
		afterHandle()
	}
	rs.Ongoing = false
	return nil
}
