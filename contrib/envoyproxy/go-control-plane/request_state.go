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
	Ctx         context.Context
	AfterHandle func()

	// HTTP components
	WrappedResponseWriter http.ResponseWriter
	FakeResponseWriter    *fakeResponseWriter

	// Body processing
	BodyBuffer *bodyBuffer

	// Processing state
	IsComplete           bool
	Blocked              bool
	AwaitingRequestBody  bool
	AwaitingResponseBody bool
}

// newRequestState creates a new request state
func newRequestState(ctx context.Context, afterHandle func(),
	fakeWriter *fakeResponseWriter, wrappedWriter http.ResponseWriter) *requestState {
	return &requestState{
		Ctx:                   ctx,
		AfterHandle:           afterHandle,
		FakeResponseWriter:    fakeWriter,
		WrappedResponseWriter: wrappedWriter,
	}
}

// InitBodyBuffer initializes the body buffer
func (rs *requestState) InitBodyBuffer(sizeLimit int) {
	if rs.BodyBuffer == nil {
		rs.BodyBuffer = newBodyBuffer(sizeLimit)
	}
}

// SetBlocked marks the request as blocked and completes it.
func (rs *requestState) SetBlocked() {
	rs.Blocked = true
	rs.Complete()
}

// Complete finalizes the request processing.
func (rs *requestState) Complete() {
	if rs.AfterHandle != nil {
		// Avoid Complete recursion by clearing afterHandle before calling it
		afterHandle := rs.AfterHandle
		rs.AfterHandle = nil
		afterHandle()
	}
	rs.IsComplete = true
}
