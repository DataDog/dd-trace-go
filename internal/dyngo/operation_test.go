// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"

	"github.com/stretchr/testify/require"
)

// Dummy struct to mimic real-life operation stacks.
type (
	RootArgs        struct{}
	RootResult      struct{}
	HTTPHandlerArgs struct {
		URL     *url.URL
		Headers http.Header
	}
	HTTPHandlerResult struct{}
	SQLQueryArg       struct {
		Query string
	}
	SQLQueryResult struct {
		Err error
	}
	GRPCHandlerArg struct {
		Msg interface{}
	}
	GRPCHandlerResult struct {
		Res interface{}
	}
	JSONParserArg struct {
		Buf []byte
	}
	JSONParserResults struct {
		Value interface{}
		Err   error
	}
	BodyReadArg struct{}
	BodyReadRes struct {
		Buf []byte
		Err error
	}
	RawBodyData []byte
)

func init() {
	dyngo.RegisterOperation(RootArgs{}, RootResult{})
	dyngo.RegisterOperation(HTTPHandlerArgs{}, HTTPHandlerResult{})
	dyngo.RegisterOperation(SQLQueryArg{}, SQLQueryResult{})
	dyngo.RegisterOperation(GRPCHandlerArg{}, GRPCHandlerResult{})
	dyngo.RegisterOperation(JSONParserArg{}, JSONParserResults{})
	dyngo.RegisterOperation(BodyReadArg{}, BodyReadRes{})
}

func TestUsage(t *testing.T) {
	t.Run("operation stacking", func(t *testing.T) {
		// Dummy waf looking for the string `attack` in HTTPHandlerArgs
		wafListener := func(called *int, blocked *bool) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(op *dyngo.Operation, args HTTPHandlerArgs) {
				*called++

				if strings.Contains(args.URL.RawQuery, "attack") {
					*blocked = true
					return
				}
				for _, values := range args.Headers {
					for _, v := range values {
						if strings.Contains(v, "attack") {
							*blocked = true
							return
						}
					}
				}

				op.OnData(func(body RawBodyData) {
					if strings.Contains(string(body), "attack") {
						*blocked = true
					}
				})
			})
		}

		// HTTP body read listener appending the read results to a buffer
		rawBodyListener := func(called *int, buf *[]byte) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(op *dyngo.Operation, args HTTPHandlerArgs) {

				op.OnFinish(func(op *dyngo.Operation, res BodyReadRes) {
					*called++
					*buf = append(*buf, res.Buf...)
					if res.Err == io.EOF {
						op.EmitData(RawBodyData(*buf))
					}
				})
			})
		}

		jsonBodyValueListener := func(called *int, value *interface{}) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(op *dyngo.Operation, args HTTPHandlerArgs) {
				didBodyRead := false
				op.OnFinish(func(op *dyngo.Operation, res JSONParserResults) {
					*called++
					if !didBodyRead || res.Err != nil {
						return
					}
					*value = res.Value
				})
				op.OnStart(func(_ *dyngo.Operation, res BodyReadArg) {
					didBodyRead = true
				})
			})
		}

		t.Run("stack monitored and not blocked by waf", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, []byte("my raw body"), RawBodyBuf)
			require.Equal(t, 4, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, []interface{}{"a", "json", "array"}, JSONBodyParserValue)
		})

		t.Run("stack monitored and blocked by waf via the http operation monitoring", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=attack"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: "a string", Err: errors.New("an error")}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("another ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: nil}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte(" value"), Err: io.EOF}, nil)
					})

					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called and blocked
			require.True(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, 5, RawBodyCalled)
			require.Equal(t, []byte("another raw body value"), RawBodyBuf)

			// The json body value listener has been called but no value due to a parser error
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, nil, JSONBodyParserValue)
		})

		t.Run("stack monitored and blocked by waf via the raw body monitoring", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: "a string"}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("an ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("att")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("a")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("ck"), Err: io.EOF}, nil)
					})

					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called and blocked
			require.True(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, 4, RawBodyCalled)
			require.Equal(t, []byte("an attack"), RawBodyBuf)

			// The json body value listener has been called but no value due to a parser error
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, "a string", JSONBodyParserValue)
		})

		t.Run("stack not monitored", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				GRPCHandlerArg{}, GRPCHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 0, WAFCalled)

			// The raw body listener has been called
			require.Nil(t, RawBodyBuf)
			require.Equal(t, 0, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 0, JSONBodyParserCalled)
			require.Nil(t, JSONBodyParserValue)
		})
	})

	t.Run("recursive operation", func(t *testing.T) {
		root := dyngo.StartOperation(RootArgs{})
		defer root.Finish(RootResult{})

		called := 0
		root.OnStart(func(HTTPHandlerArgs) {
			called++
		})

		operation(root, HTTPHandlerArgs{}, HTTPHandlerResult{}, func(o *dyngo.Operation) {
			operation(o, HTTPHandlerArgs{}, HTTPHandlerResult{}, func(o *dyngo.Operation) {
				operation(o, HTTPHandlerArgs{}, HTTPHandlerResult{}, func(o *dyngo.Operation) {
					operation(o, HTTPHandlerArgs{}, HTTPHandlerResult{}, func(o *dyngo.Operation) {
						operation(o, HTTPHandlerArgs{}, HTTPHandlerResult{}, func(*dyngo.Operation) {
						})
					})
				})
			})
		})

		require.Equal(t, 5, called)
	})
}

type (
	MyOperationArgs struct{}
	MyOperationData struct{}
	MyOperationRes  struct{}
)

func init() {
	dyngo.RegisterOperation(MyOperationArgs{}, MyOperationRes{})
}

func TestRegisterUnregister(t *testing.T) {
	var onStartCalled, onDataCalled, onFinishCalled int

	unregister := dyngo.Register(
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener(func(*dyngo.Operation, MyOperationArgs) {
					onStartCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener(func(*dyngo.Operation, MyOperationArgs) {
					onDataCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnFinishEventListener(func(*dyngo.Operation, MyOperationRes) {
					onFinishCalled++
				}),
			},
		},
	)

	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

	unregister()
	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

}

func TestTypeSafety(t *testing.T) {
	t.Run("invalid operation types", func(t *testing.T) {
		type (
			myOpArg struct{}
			myOpRes struct{}
		)

		require.Panics(t, func() {
			dyngo.RegisterOperation(nil, nil)
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, nil)
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(nil, myOpRes{})
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation("not ok", myOpRes{})
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, "not ok")
		})
		type myInterface interface{}
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, myInterface(nil))
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myInterface(nil), myOpRes{})
		})
	})

	t.Run("multiple operation registration", func(t *testing.T) {
		type (
			myOp1Arg struct{}
			myOp1Res struct{}
		)

		require.NotPanics(t, func() {
			dyngo.RegisterOperation(myOp1Arg{}, myOp1Res{})
		})
		require.Panics(t, func() {
			// Already registered
			dyngo.RegisterOperation(myOp1Arg{}, myOp1Res{})
		})

		require.NotPanics(t, func() {
			dyngo.RegisterOperation((*myOp1Arg)(nil), (*myOp1Res)(nil))
		})
		require.Panics(t, func() {
			// Already registered
			dyngo.RegisterOperation((*myOp1Arg)(nil), (*myOp1Res)(nil))
		})
	})

	t.Run("operation usage before registration", func(t *testing.T) {
		type (
			myOp2Arg struct{}
			myOp2Res struct{}

			myOp3Arg struct{}
			myOp3Res struct{}
		)

		require.Panics(t, func() {
			dyngo.StartOperation(myOp2Arg{})
		})

		dyngo.RegisterOperation(myOp2Arg{}, myOp2Res{})

		t.Run("finishing with the expected result type", func(t *testing.T) {
			require.NotPanics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				// Finish with the expected result type
				op.Finish(myOp2Res{})
			})
		})

		t.Run("finishing with the wrong operation result type", func(t *testing.T) {
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				// Finish with the wrong result type
				op.Finish(&myOp2Res{})
			})
		})

		t.Run("starting an operation with the wrong operation argument type", func(t *testing.T) {
			require.Panics(t, func() {
				// Start with the wrong argument type
				dyngo.StartOperation(myOp2Res{})
			})
		})

		t.Run("listening to an operation not yet registered", func(t *testing.T) {
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnStart(func(myOp3Arg) {})
			})
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnFinish(func(myOp3Res) {})
			})
			require.NotPanics(t, func() {
				dyngo.RegisterOperation(myOp3Arg{}, myOp3Res{})
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnStart(func(myOp3Arg) {})
				op.OnFinish(func(myOp3Res) {})
			})
		})
	})

	t.Run("event listener types", func(t *testing.T) {
		type (
			myOp4Arg  struct{}
			myOp4Res  struct{}
			myOp4Data struct{}
		)

		dyngo.RegisterOperation(myOp4Arg{}, myOp4Res{})

		op := dyngo.StartOperation(myOp4Arg{})
		defer op.Finish(myOp4Res{})

		t.Run("valid listeners", func(t *testing.T) {
			require.NotPanics(t, func() {
				// Start listeners with and without the operation pointer
				op.OnStart(func(*dyngo.Operation, myOp4Arg) {})
				op.OnStart(func(myOp4Arg) {})

				// Finish listeners with and without the operation pointer
				op.OnFinish(func(myOp4Res) {})
				op.OnFinish(func(*dyngo.Operation, myOp4Res) {})

				// Data listeners with and without the operation pointer
				op.OnData(func(myOp4Data) {})
				op.OnData(func(*dyngo.Operation, myOp4Data) {})
			})
		})

		t.Run("invalid listeners", func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				listener interface{}
			}{
				{
					name:     "nil value",
					listener: nil,
				},
				{
					name:     "not a function",
					listener: "not a function",
				},
				{
					name:     "no arguments",
					listener: func() {},
				},
				{
					name:     "missing event payload",
					listener: func(*dyngo.Operation) {},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					require.Panics(t, func() {
						op.OnStart(tc.listener)
					})
					require.Panics(t, func() {
						op.OnData(tc.listener)
					})
					require.Panics(t, func() {
						op.OnFinish(tc.listener)
					})
				})
			}
		})

		t.Run("invalid argument order", func(t *testing.T) {
			require.Panics(t, func() {
				op.OnStart(func(myOp4Arg, *dyngo.Operation) {})
			})
			require.Panics(t, func() {
				op.OnFinish(func(myOp4Res, *dyngo.Operation) {})
			})
			require.Panics(t, func() {
				op.OnFinish(func(myOp4Data, *dyngo.Operation) {})
			})
		})

		t.Run("invalid operation argument type", func(t *testing.T) {
			t.Run("not a pointer", func(t *testing.T) {
				require.Panics(t, func() {
					op.OnStart(func(dyngo.Operation, myOp4Arg) {})
				})
				require.Panics(t, func() {
					op.OnFinish(func(dyngo.Operation, myOp4Res) {})
				})
				require.Panics(t, func() {
					op.OnFinish(func(dyngo.Operation, myOp4Data) {})
				})
			})

			t.Run("pointer to pointer", func(t *testing.T) {
				require.Panics(t, func() {
					op.OnStart(func(**dyngo.Operation, myOp4Arg) {})
				})
				require.Panics(t, func() {
					op.OnFinish(func(**dyngo.Operation, myOp4Res) {})
				})
				require.Panics(t, func() {
					op.OnFinish(func(**dyngo.Operation, myOp4Data) {})
				})
			})
		})

	})
}

// TODO(julio): dispatch time benchmark

// TODO(julio): concurrency test

func operation(parent *dyngo.Operation, args, res interface{}, child func(*dyngo.Operation)) {
	op := dyngo.StartOperation(args, dyngo.WithParent(parent))
	defer op.Finish(res)
	if child != nil {
		child(op)
	}
}
