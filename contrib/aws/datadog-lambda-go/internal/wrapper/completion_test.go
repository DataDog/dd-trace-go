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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

func TestRuntimeCompletion(t *testing.T) {
	lateError := errors.New("late read failure")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"EOF", io.EOF},
		{"read error", lateError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finishes := 0
			listener := runtimeListener{
				start: func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
				finish: func(_ context.Context, err error) {
					finishes++
					if tc.err == io.EOF {
						assert.NoError(t, err)
					} else {
						assert.Same(t, tc.err, err)
					}
				},
			}
			wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
				return readFunc(func(p []byte) (int, error) {
					assert.Zero(t, finishes, "stream work must precede completion")
					assert.NotNil(t, CurrentContext)
					return copy(p, "response bytes"), tc.err
				}), nil
			}, listener)
			result, err := wrapped(context.Background(), nil)
			require.NoError(t, err)
			assert.Zero(t, finishes, "returning a reader does not finish the invocation")
			body, err := io.ReadAll(result)
			assert.Equal(t, "response bytes", string(body))
			if tc.err == io.EOF {
				assert.NoError(t, err)
			} else {
				assert.Same(t, tc.err, err)
			}
			assert.Zero(t, finishes, "AWS's response POST is still in progress at EOF")
			assert.NotNil(t, CurrentContext)
			assert.NoError(t, result.(io.Closer).Close())
			assert.Equal(t, 1, finishes)
			assert.Nil(t, CurrentContext)
			assert.NoError(t, result.(io.Closer).Close())
			assert.Equal(t, 1, finishes)
		})
	}
}

type invokeFunc func(context.Context, []byte) ([]byte, error)

func (f invokeFunc) Invoke(ctx context.Context, p []byte) ([]byte, error) { return f(ctx, p) }

func TestPanicCompletion(t *testing.T) {
	for _, variant := range []string{"runtime", "function", "interface"} {
		t.Run(variant, func(t *testing.T) {
			finishes := 0
			want := errors.New("handler panic")
			listener := runtimeListener{
				start: func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) {
					assert.Equal(t, finishes == 0, ctx.Value("cold_start"))
					return ctx, msg
				},
				finish: func(_ context.Context, err error) {
					finishes++
					assert.ErrorContains(t, err, "handler panic")
				},
			}
			var invoke func()
			switch variant {
			case "runtime":
				wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) { panic(want) }, listener)
				invoke = func() { _, _ = wrapped(context.Background(), nil) }
			case "function":
				wrapped := WrapHandlerWithListeners(func() error { panic(want) }, listener).(wrappedFunc)
				invoke = func() { _, _ = wrapped(context.Background(), nil) }
			case "interface":
				wrapped := WrapHandlerInterfaceWithListeners(invokeFunc(func(context.Context, []byte) ([]byte, error) { panic(want) }), listener)
				invoke = func() { _, _ = wrapped.Invoke(context.Background(), nil) }
			}
			for i := 1; i <= 2; i++ {
				assert.PanicsWithValue(t, want, invoke)
				assert.Equal(t, i, finishes)
				assert.Nil(t, CurrentContext)
			}
		})
	}
}

// AWS serializes readers with exported fields as JSON. The manual wrapper must
// keep the original response type so that AWS can make that decision.
func TestManualReaderSerialization(t *testing.T) {
	type response struct {
		io.Reader `json:"-"`
		Value     string `json:"value"`
	}
	handler := func() (any, error) { return response{strings.NewReader("stream"), "<json>"}, nil }
	wrapped := lambda.NewHandlerWithOptions(WrapHandlerWithListeners(handler), lambda.WithSetEscapeHTML(false))
	body, err := wrapped.Invoke(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":"<json>"}`, string(body))
	assert.Contains(t, string(body), "<json>")
}

type closeResponse struct {
	io.Reader
	close func() error
}

func (r closeResponse) Close() error { return r.close() }

func TestRuntimeResponseClose(t *testing.T) {
	want := errors.New("close failed")
	for _, tc := range []struct {
		name                string
		read, cancel        bool
		closeErr, finishErr error
	}{
		{name: "after EOF", read: true},
		{name: "close error after EOF", read: true, closeErr: want, finishErr: want},
		{name: "abandoned", finishErr: io.ErrUnexpectedEOF},
		{name: "close error", closeErr: want, finishErr: want},
		{name: "canceled", cancel: true, finishErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finishes, closes := 0, 0
			wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
				return closeResponse{strings.NewReader("bytes"), func() error { closes++; return tc.closeErr }}, nil
			}, runtimeListener{
				start: func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
				finish: func(_ context.Context, err error) {
					finishes++
					assert.ErrorIs(t, err, tc.finishErr)
				},
			})
			result, err := wrapped(ctx, nil)
			require.NoError(t, err)
			_, hasContentType := result.(interface{ ContentType() string })
			assert.False(t, hasContentType)
			if tc.read {
				body, err := io.ReadAll(result)
				require.NoError(t, err)
				assert.Equal(t, "bytes", string(body))
				_, err = result.Read(make([]byte, 1))
				assert.ErrorIs(t, err, io.EOF)
			}
			assert.Zero(t, closes)
			if tc.cancel {
				cancel()
			}
			assert.ErrorIs(t, result.(io.Closer).Close(), tc.closeErr)
			assert.Equal(t, 1, closes)
			assert.Equal(t, 1, finishes)
			assert.Nil(t, CurrentContext)
		})
	}
}

func TestRuntimeResponsePanic(t *testing.T) {
	for _, operation := range []string{"Read", "Close"} {
		t.Run(operation, func(t *testing.T) {
			want := &struct{ Message string }{"stream panic"}
			finishes := 0
			wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
				return closeResponse{
					readFunc(func([]byte) (int, error) { panic(want) }),
					func() error {
						if operation == "Close" {
							panic(want)
						}
						return nil
					},
				}, nil
			}, runtimeListener{
				start:  func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
				finish: func(_ context.Context, err error) { finishes++; assert.ErrorContains(t, err, "stream panic") },
			})
			result, err := wrapped(context.Background(), nil)
			require.NoError(t, err)
			assert.PanicsWithValue(t, want, func() {
				if operation == "Read" {
					_, _ = result.Read(make([]byte, 1))
				} else {
					_ = result.(io.Closer).Close()
				}
			})
			assert.Equal(t, 1, finishes)
			assert.Nil(t, CurrentContext)
		})
	}
}

func TestRuntimeConcurrentCompletion(t *testing.T) {
	var finishes atomic.Int32
	wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
		return readFunc(func([]byte) (int, error) { return 0, io.EOF }), nil
	}, runtimeListener{
		start:  func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
		finish: func(context.Context, error) { finishes.Add(1) },
	})
	result, err := wrapped(context.Background(), nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = result.Read(make([]byte, 1)) })
	wg.Go(func() { _ = result.(io.Closer).Close() })
	wg.Wait()
	assert.EqualValues(t, 1, finishes.Load())
	assert.Nil(t, CurrentContext)
}

func TestManualInterfaceReaderCompletion(t *testing.T) {
	want := errors.New("late read error")
	finishes := 0
	handler := lambda.NewHandler(func() (io.Reader, error) {
		return readFunc(func([]byte) (int, error) {
			assert.Zero(t, finishes)
			return 0, want
		}), nil
	})
	wrapped := WrapHandlerInterfaceWithListeners(handler, runtimeListener{
		start:  func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
		finish: func(_ context.Context, err error) { finishes++; assert.ErrorIs(t, err, want) },
	})
	_, err := wrapped.Invoke(context.Background(), nil)
	assert.ErrorIs(t, err, want)
	assert.Equal(t, 1, finishes)
}

func TestRuntimeCompletionWithoutResponse(t *testing.T) {
	finishes := 0
	wrapped := WrapRuntimeHandlerWithListeners(func(context.Context, []byte) (io.Reader, error) {
		return nil, nil
	}, runtimeListener{
		start:  func(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) { return ctx, msg },
		finish: func(_ context.Context, err error) { finishes++; assert.NoError(t, err) },
	})
	result, err := wrapped(context.Background(), nil)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Equal(t, 1, finishes)
	assert.Nil(t, CurrentContext)
}
