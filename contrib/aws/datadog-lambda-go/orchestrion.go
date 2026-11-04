// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddlambda

import (
	"context"
	"encoding/json"
	"io"
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/wrapper"
)

// setupOrchestrion and invokeOrchestrion are called through go:linkname from
// the Orchestrion aspect injected into aws-lambda-go's start function. They
// stay unexported so they are not part of the public API. The signatures have
// no variadic arguments so the injected declarations can reference them.

//go:linkname setupOrchestrion
func setupOrchestrion() {
	setupAppSec()
	orchListeners = initializeListeners(nil)
}

//go:linkname invokeOrchestrion
func invokeOrchestrion(ctx context.Context, payload []byte, next func(context.Context, []byte) (io.Reader, error)) (io.Reader, error) {
	cold := orchCold
	orchCold = false

	//nolint
	ctx = context.WithValue(ctx, "cold_start", cold)
	msg := json.RawMessage(payload)
	for _, listener := range orchListeners {
		ctx, msg = listener.HandlerStarted(ctx, msg)
	}
	wrapper.CurrentContext = ctx
	reader, err := next(ctx, msg)
	ctx = withLambdaResponse(ctx, reader)
	for _, listener := range orchListeners {
		listener.HandlerFinished(ctx, err)
	}
	wrapper.CurrentContext = nil
	return reader, err
}

var (
	orchCold      = true
	orchListeners []wrapper.HandlerListener
)

// byteBuffer is implemented by the buffer Lambda uses for an ordinary JSON
// response. Bytes does not advance the reader, so the runtime can still send it.
type byteBuffer interface {
	Bytes() []byte
}

// withLambdaResponse stores a copy of a buffered JSON response for the
// extension's end-invocation POST. A streaming reader is left unread, and the
// extension receives an empty object, matching WrapFunction.
func withLambdaResponse(ctx context.Context, reader io.Reader) context.Context {
	if reader == nil {
		return ctx
	}
	if buf, ok := reader.(byteBuffer); ok {
		raw := append([]byte(nil), buf.Bytes()...)
		return context.WithValue(ctx, extension.DdLambdaResponse, json.RawMessage(raw))
	}
	return context.WithValue(ctx, extension.DdLambdaResponse, struct{}{})
}
