// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wrapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
)

type listenerKey string

// recordingListener appends "<name>.started" and "<name>.finished" to a
// shared event log, and keeps what it saw for later assertions.
type recordingListener struct {
	name   string
	events *[]string

	// msgOut replaces the payload in HandlerStarted when it is not nil.
	msgOut json.RawMessage

	startedCtx  context.Context
	startedMsg  json.RawMessage
	finishedCtx context.Context
	finishedErr error
}

func (l *recordingListener) HandlerStarted(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) {
	*l.events = append(*l.events, l.name+".started")
	l.startedCtx, l.startedMsg = ctx, msg
	ctx = context.WithValue(ctx, listenerKey(l.name), true)
	if l.msgOut != nil {
		msg = l.msgOut
	}
	return ctx, msg
}

func (l *recordingListener) HandlerFinished(ctx context.Context, err error) {
	*l.events = append(*l.events, l.name+".finished")
	l.finishedCtx, l.finishedErr = ctx, err
}

func okNext(context.Context, []byte) (io.Reader, error) {
	return bytes.NewBufferString(`"ok"`), nil
}

func TestWrapHandlerFuncColdStart(t *testing.T) {
	var events []string
	l := &recordingListener{name: "a", events: &events}
	invoke := WrapHandlerFuncWithListeners(okNext, l)

	cold := make([]any, 0, 3)
	for range 3 {
		_, err := invoke(context.Background(), []byte(`{}`))
		require.NoError(t, err)
		cold = append(cold, l.startedCtx.Value("cold_start"))
	}
	require.Equal(t, []any{true, false, false}, cold)
}

func TestWrapHandlerFuncListenerOrder(t *testing.T) {
	var events []string
	invoke := WrapHandlerFuncWithListeners(func(ctx context.Context, p []byte) (io.Reader, error) {
		events = append(events, "next")
		return okNext(ctx, p)
	},
		&recordingListener{name: "a", events: &events},
		&recordingListener{name: "b", events: &events},
	)

	_, err := invoke(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a.started", "b.started", "next", "a.finished", "b.finished"}, events)
}

func TestWrapHandlerFuncChainsListeners(t *testing.T) {
	var events []string
	a := &recordingListener{name: "a", events: &events, msgOut: json.RawMessage(`{"from":"a"}`)}
	b := &recordingListener{name: "b", events: &events, msgOut: json.RawMessage(`{"from":"b"}`)}
	var nextCtx context.Context
	var nextPayload []byte
	invoke := WrapHandlerFuncWithListeners(func(ctx context.Context, p []byte) (io.Reader, error) {
		nextCtx, nextPayload = ctx, p
		return okNext(ctx, p)
	}, a, b)

	_, err := invoke(context.Background(), []byte(`{"from":"runtime"}`))
	require.NoError(t, err)

	// Each step gets the previous step's payload...
	require.JSONEq(t, `{"from":"runtime"}`, string(a.startedMsg))
	require.JSONEq(t, `{"from":"a"}`, string(b.startedMsg))
	require.JSONEq(t, `{"from":"b"}`, string(nextPayload))
	// ...and the context values added before it.
	require.Equal(t, true, b.startedCtx.Value(listenerKey("a")))
	require.Equal(t, true, nextCtx.Value(listenerKey("a")))
	require.Equal(t, true, nextCtx.Value(listenerKey("b")))
}

func TestWrapHandlerFuncCurrentContext(t *testing.T) {
	var events []string
	var during context.Context
	var handlerCtx context.Context
	invoke := WrapHandlerFuncWithListeners(func(ctx context.Context, p []byte) (io.Reader, error) {
		handlerCtx, during = ctx, CurrentContext
		return okNext(ctx, p)
	}, &recordingListener{name: "a", events: &events})

	_, err := invoke(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, handlerCtx, during)
	require.Nil(t, CurrentContext)
}

func TestWrapHandlerFuncReturnsNextResult(t *testing.T) {
	var events []string
	l := &recordingListener{name: "a", events: &events}

	t.Run("response", func(t *testing.T) {
		want := strings.NewReader("body")
		invoke := WrapHandlerFuncWithListeners(func(context.Context, []byte) (io.Reader, error) {
			return want, nil
		}, l)
		got, err := invoke(context.Background(), []byte(`{}`))
		require.NoError(t, err)
		require.Same(t, want, got)
		require.NoError(t, l.finishedErr)
	})

	t.Run("error", func(t *testing.T) {
		boom := errors.New("boom")
		invoke := WrapHandlerFuncWithListeners(func(context.Context, []byte) (io.Reader, error) {
			return nil, boom
		}, l)
		got, err := invoke(context.Background(), []byte(`{}`))
		require.Nil(t, got)
		require.Same(t, boom, err)
		require.Same(t, boom, l.finishedErr)
	})
}

// The extension reads the response from the context passed to HandlerFinished.
func TestWrapHandlerFuncResponseReachesListeners(t *testing.T) {
	var events []string
	l := &recordingListener{name: "a", events: &events}

	_, err := WrapHandlerFuncWithListeners(okNext, l)(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	raw, ok := l.finishedCtx.Value(extension.DdLambdaResponse).(json.RawMessage)
	require.True(t, ok)
	require.JSONEq(t, `"ok"`, string(raw))
}

func TestWithLambdaResponse(t *testing.T) {
	t.Run("buffered", func(t *testing.T) {
		buf := bytes.NewBufferString(`{"ok":true}`)
		ctx := withLambdaResponse(context.Background(), buf)

		raw, ok := ctx.Value(extension.DdLambdaResponse).(json.RawMessage)
		require.True(t, ok)
		require.JSONEq(t, `{"ok":true}`, string(raw))
		got, err := io.ReadAll(buf)
		require.NoError(t, err)
		require.Equal(t, `{"ok":true}`, string(got))
	})

	t.Run("copy is independent of the buffer", func(t *testing.T) {
		buf := bytes.NewBufferString(`{"ok":true}`)
		ctx := withLambdaResponse(context.Background(), buf)

		// aws-lambda-go resets and reuses its response buffer on the next invocation.
		buf.Reset()
		buf.WriteString(`{"ok":false}`)
		raw := ctx.Value(extension.DdLambdaResponse).(json.RawMessage)
		require.JSONEq(t, `{"ok":true}`, string(raw))
	})

	t.Run("raw bytes that are not JSON", func(t *testing.T) {
		ctx := withLambdaResponse(context.Background(), bytes.NewBufferString("handler-ok"))

		raw, ok := ctx.Value(extension.DdLambdaResponse).(json.RawMessage)
		require.True(t, ok)
		require.Equal(t, "handler-ok", string(raw))
		// The extension marshals the value and falls back to {} on error.
		_, err := json.Marshal(ctx.Value(extension.DdLambdaResponse))
		require.Error(t, err)
	})

	t.Run("stream", func(t *testing.T) {
		reader := strings.NewReader("streamed-body")
		ctx := withLambdaResponse(context.Background(), reader)

		body, err := json.Marshal(ctx.Value(extension.DdLambdaResponse))
		require.NoError(t, err)
		require.JSONEq(t, `{}`, string(body))
		got, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, "streamed-body", string(got))
	})

	t.Run("nil reader", func(t *testing.T) {
		ctx := context.Background()
		require.Equal(t, ctx, withLambdaResponse(ctx, nil))
	})
}
