// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package proxy

import (
	"context"
	"errors"
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
	Context     context.Context
	afterHandle func()

	// HTTP components
	wrappedResponseWriter http.ResponseWriter
	fakeResponseWriter    *fakeResponseWriter

	// Body processing
	requestBuffer  *bodyBuffer
	responseBuffer *bodyBuffer

	// Processing state
	State MessageType
}

// newRequestState creates a new request state
func newRequestState(request *http.Request, bodyLimit int, framework string, options ...tracer.StartSpanOption) (RequestState, bool) {
	fakeResponseWriter := newFakeResponseWriter()
	wrappedResponseWriter, spanRequest, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
		Framework: framework,
		Resource:  request.Method + " " + path.Clean(request.URL.Path),
		SpanOpts:  append(options, tracer.Tag(ext.SpanKind, ext.SpanKindServer)),
	}, fakeResponseWriter, request)

	var requestBuffer *bodyBuffer
	if bodyLimit > 0 {
		requestBuffer = newBodyBuffer(bodyLimit)
	}

	var responseBuffer *bodyBuffer
	if bodyLimit > 0 {
		responseBuffer = newBodyBuffer(bodyLimit)
	}

	return RequestState{
		Context:               spanRequest.Context(),
		afterHandle:           afterHandle,
		fakeResponseWriter:    fakeResponseWriter,
		wrappedResponseWriter: wrappedResponseWriter,
		requestBuffer:         requestBuffer,
		responseBuffer:        responseBuffer,
		State:                 MessageTypeRequestHeaders,
	}, blocked
}

// PropagationHeaders creates header mutations for trace propagation
func (rs *RequestState) PropagationHeaders() (http.Header, error) {
	span, ok := tracer.SpanFromContext(rs.Context)
	if !ok {
		return nil, errors.New("no span found in context")
	}

	newHeaders := make(http.Header)
	if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, err
	}

	return newHeaders, nil
}

// BlockAction marks the request as blocked and completes it.
func (rs *RequestState) BlockAction() BlockActionOptions {
	rs.Close()
	if rs.fakeResponseWriter.status == 0 {
		panic("cannot block request without a status code")
	}

	return BlockActionOptions{
		StatusCode: rs.fakeResponseWriter.status,
		Headers:    rs.fakeResponseWriter.headers,
		Body:       rs.fakeResponseWriter.body,
	}
}

// Close finalizes the request processing.
func (rs *RequestState) Close() error {
	if rs.afterHandle != nil {
		// Avoid Complete recursion by clearing afterHandle before calling it
		afterHandle := rs.afterHandle
		rs.afterHandle = nil
		afterHandle()
	}

	if rs.State.Ongoing() {
		rs.State = MessageTypeFinished
	}
	return nil
}

func (rs *RequestState) Span() (*tracer.Span, bool) {
	return tracer.SpanFromContext(rs.Context)
}
