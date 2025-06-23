// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"net/http"
)

// requestState manages the state of a single request through its lifecycle
type requestState struct {
	ctx         context.Context
	afterHandle func()

	// HTTP components
	fakeResponseWriter    *fakeResponseWriter
	wrappedResponseWriter http.ResponseWriter

	bodyBuffer *BodyBuffer

	// Processing state
	isComplete           bool
	blocked              bool
	awaitingResponseBody bool
}

// newRequestState creates a new request state
func newRequestState(ctx context.Context, afterHandle func(),
	fakeWriter *fakeResponseWriter, wrappedWriter http.ResponseWriter) *requestState {
	return &requestState{
		ctx:                   ctx,
		afterHandle:           afterHandle,
		fakeResponseWriter:    fakeWriter,
		wrappedResponseWriter: wrappedWriter,
	}
}

// InitBodyBuffer initializes the body buffer
func (rs *requestState) InitBodyBuffer(sizeLimit int) {
	if rs.bodyBuffer == nil {
		rs.bodyBuffer = NewBodyBuffer(sizeLimit)
	}
}

// Context returns the request context
func (rs *requestState) Context() context.Context {
	return rs.ctx
}

// SetBlocked marks the request as blocked
func (rs *requestState) SetBlocked() {
	rs.blocked = true
	rs.Complete()
}

// IsBlocked returns whether the request is blocked
func (rs *requestState) IsBlocked() bool {
	return rs.blocked
}

// SetAwaitingResponseBody marks that we are waiting for response body
func (rs *requestState) SetAwaitingResponseBody() {
	rs.awaitingResponseBody = true
}

// IsAwaitingResponseBody returns whether we are waiting for response body
func (rs *requestState) IsAwaitingResponseBody() bool {
	return rs.awaitingResponseBody
}

// Complete finalizes the request processing
func (rs *requestState) Complete() {
	if rs.afterHandle != nil {
		// Avoid Complete recursion by clearing afterHandle before calling it
		afterHandle := rs.afterHandle
		rs.afterHandle = nil
		afterHandle()
	}
	rs.isComplete = true
}

// IsComplete returns whether the request processing is complete
func (rs *requestState) IsComplete() bool {
	return rs.isComplete
}

// GetFakeResponseWriter returns the fake response writer
func (rs *requestState) GetFakeResponseWriter() *fakeResponseWriter {
	return rs.fakeResponseWriter
}

// GetWrappedResponseWriter returns the wrapped response writer
func (rs *requestState) GetWrappedResponseWriter() http.ResponseWriter {
	return rs.wrappedResponseWriter
}
