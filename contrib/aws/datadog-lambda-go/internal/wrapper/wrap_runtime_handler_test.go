// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wrapper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/lambda/handlertrace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
)

type runtimeListener struct {
	start  func(context.Context, json.RawMessage) (context.Context, json.RawMessage)
	finish func(context.Context, error)
}

func (l runtimeListener) HandlerStarted(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) {
	return l.start(ctx, msg)
}

func (l runtimeListener) HandlerFinished(ctx context.Context, err error) {
	l.finish(ctx, err)
}

type runtimeResponse struct {
	*strings.Reader
	closed bool
}

func (r *runtimeResponse) Close() error      { r.closed = true; return nil }
func (*runtimeResponse) ContentType() string { return "application/custom" }

func TestWrapRuntimeHandlerWithListeners(t *testing.T) {
	type contextKey struct{}
	response := &struct{ Value string }{Value: "<response>"}
	reader := &runtimeResponse{Reader: strings.NewReader("encoded by AWS")}
	requestCallbacks, responseCallbacks, finishes := 0, 0, 0
	ctx := handlertrace.NewContext(context.WithValue(context.Background(), contextKey{}, "configured"), handlertrace.HandlerTrace{
		RequestEvent: func(_ context.Context, _ any) { requestCallbacks++ },
		ResponseEvent: func(_ context.Context, event any) {
			responseCallbacks++
			assert.Same(t, response, event)
		},
	})
	listener := runtimeListener{
		start: func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) {
			assert.Equal(t, finishes == 0, ctx.Value("cold_start"))
			assert.Equal(t, "configured", ctx.Value(contextKey{}))
			assert.JSONEq(t, `{"original":true}`, string(msg))
			return context.WithValue(ctx, contextKey{}, "listener"), json.RawMessage(`{"stripped":true}`)
		},
		finish: func(ctx context.Context, err error) {
			assert.NoError(t, err)
			assert.Same(t, response, ctx.Value(extension.DdLambdaResponse))
			finishes++
		},
	}
	wrapped := WrapRuntimeHandlerWithListeners(func(ctx context.Context, payload []byte) (io.Reader, error) {
		assert.Equal(t, "listener", ctx.Value(contextKey{}))
		assert.Equal(t, "listener", CurrentContext.Value(contextKey{}))
		assert.JSONEq(t, `{"stripped":true}`, string(payload))
		// These callbacks are normally invoked by AWS's adapter, after decoding
		// the request and before encoding the response.
		trace := handlertrace.FromContext(ctx)
		trace.RequestEvent(ctx, map[string]bool{"stripped": true})
		trace.ResponseEvent(ctx, response)
		return reader, nil
	}, listener)
	for range 2 {
		result, err := wrapped(ctx, []byte(`{"original":true}`))
		require.NoError(t, err)
		// Preserve the reader itself, including optional methods such as Close.
		assert.Same(t, reader, result)
		assert.False(t, reader.closed)
		assert.Equal(t, len("encoded by AWS"), reader.Len())
		assert.Nil(t, CurrentContext)
	}
	assert.Equal(t, 2, finishes)
	assert.Equal(t, 2, requestCallbacks)
	assert.Equal(t, 2, responseCallbacks)
}

func TestWrapRuntimeHandlerWithListenersError(t *testing.T) {
	want := errors.New("AWS decoding failed")
	finished := false
	wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
		return nil, want
	}, runtimeListener{
		start: func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
		finish: func(ctx context.Context, err error) {
			assert.ErrorIs(t, err, want)
			assert.Nil(t, ctx.Value(extension.DdLambdaResponse))
			finished = true
		},
	})
	result, err := wrapped(context.Background(), nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, want)
	assert.True(t, finished)
	assert.Nil(t, CurrentContext)
}

func TestWrapRuntimeHandlerWithListenersPanic(t *testing.T) {
	wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
		panic("handler panic")
	})
	assert.PanicsWithValue(t, "handler panic", func() { _, _ = wrapped(context.Background(), nil) })
	assert.Nil(t, CurrentContext)
}
