// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Dummy struct to mimic real-life operation stacks.
type (
	RootArgs struct{}
	RootRes  struct{}
)

type (
	HTTPHandlerArgs struct {
		URL     *url.URL
		Headers http.Header
	}
	HTTPHandlerRes               struct{}
	OnHTTPHandlerOperationStart  func(dyngo.Operation, HTTPHandlerArgs)
	OnHTTPHandlerOperationFinish func(dyngo.Operation, HTTPHandlerRes)
)

func (f OnHTTPHandlerOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*HTTPHandlerArgs)(nil)).Elem()
}
func (f OnHTTPHandlerOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(HTTPHandlerArgs))
}
func (f OnHTTPHandlerOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*HTTPHandlerRes)(nil)).Elem()
}
func (f OnHTTPHandlerOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(HTTPHandlerRes))
}

type (
	SQLQueryArgs struct {
		Query string
	}
	SQLQueryRes struct {
		Err error
	}
	OnSQLQueryOperationStart  func(dyngo.Operation, SQLQueryArgs)
	OnSQLQueryOperationFinish func(dyngo.Operation, SQLQueryRes)
)

func (f OnSQLQueryOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*SQLQueryArgs)(nil)).Elem()
}
func (f OnSQLQueryOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(SQLQueryArgs))
}
func (f OnSQLQueryOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*SQLQueryRes)(nil)).Elem()
}
func (f OnSQLQueryOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(SQLQueryRes))
}

type (
	GRPCHandlerArgs struct {
		Msg interface{}
	}
	GRPCHandlerRes struct {
		Res interface{}
	}
	OnGRPCHandlerOperationStart  func(dyngo.Operation, GRPCHandlerArgs)
	OnGRPCHandlerOperationFinish func(dyngo.Operation, GRPCHandlerRes)
)

func (f OnGRPCHandlerOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*GRPCHandlerArgs)(nil)).Elem()
}
func (f OnGRPCHandlerOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(GRPCHandlerArgs))
}
func (f OnGRPCHandlerOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*GRPCHandlerRes)(nil)).Elem()
}
func (f OnGRPCHandlerOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(GRPCHandlerRes))
}

type (
	JSONParserArgs struct {
		Buf []byte
	}
	JSONParserRes struct {
		Value interface{}
		Err   error
	}
	OnJSONParserOperationStart  func(dyngo.Operation, JSONParserArgs)
	OnJSONParserOperationFinish func(dyngo.Operation, JSONParserRes)
)

func (f OnJSONParserOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*JSONParserArgs)(nil)).Elem()
}
func (f OnJSONParserOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(JSONParserArgs))
}
func (f OnJSONParserOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*JSONParserRes)(nil)).Elem()
}
func (f OnJSONParserOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(JSONParserRes))
}

type (
	BodyReadArgs struct{}
	BodyReadRes  struct {
		Buf []byte
		Err error
	}
	OnBodyReadOperationStart  func(dyngo.Operation, BodyReadArgs)
	OnBodyReadOperationFinish func(dyngo.Operation, BodyReadRes)
)

func (f OnBodyReadOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*BodyReadArgs)(nil)).Elem()
}
func (f OnBodyReadOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(BodyReadArgs))
}
func (f OnBodyReadOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*BodyReadRes)(nil)).Elem()
}
func (f OnBodyReadOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(BodyReadRes))
}

type (
	MyOperationArgs     struct{ n int }
	MyOperationRes      struct{ n int }
	OnMyOperationStart  func(dyngo.Operation, MyOperationArgs)
	OnMyOperationFinish func(dyngo.Operation, MyOperationRes)
)

func (f OnMyOperationStart) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperationArgs)(nil)).Elem()
}
func (f OnMyOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperationArgs))
}
func (f OnMyOperationFinish) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperationRes)(nil)).Elem()
}
func (f OnMyOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperationRes))
}

type (
	MyOperation2Args     struct{}
	MyOperation2Res      struct{}
	OnMyOperation2Start  func(dyngo.Operation, MyOperation2Args)
	OnMyOperation2Finish func(dyngo.Operation, MyOperation2Res)
)

func (f OnMyOperation2Start) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperation2Args)(nil)).Elem()
}
func (f OnMyOperation2Start) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperation2Args))
}
func (f OnMyOperation2Finish) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperation2Res)(nil)).Elem()
}
func (f OnMyOperation2Finish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperation2Res))
}

type (
	MyOperation3Args     struct{}
	MyOperation3Res      struct{}
	OnMyOperation3Start  func(dyngo.Operation, MyOperation3Args)
	OnMyOperation3Finish func(dyngo.Operation, MyOperation3Res)
)

func (f OnMyOperation3Start) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperation3Args)(nil)).Elem()
}
func (f OnMyOperation3Start) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperation3Args))
}
func (f OnMyOperation3Finish) ListenedType() reflect.Type {
	return reflect.TypeOf((*MyOperation3Res)(nil)).Elem()
}
func (f OnMyOperation3Finish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(MyOperation3Res))
}

func TestUsage(t *testing.T) {
	t.Run("operation-stacking", func(t *testing.T) {
		// HTTP body read listener appending the read results to a buffer
		rawBodyListener := func(called *int, buf *[]byte) dyngo.EventListener {
			return OnHTTPHandlerOperationStart(func(op dyngo.Operation, _ HTTPHandlerArgs) {
				op.On(OnBodyReadOperationFinish(func(op dyngo.Operation, res BodyReadRes) {
					*called++
					*buf = append(*buf, res.Buf...)
				}))
			})
		}

		// Dummy waf looking for the string `attack` in HTTPHandlerArgs
		wafListener := func(called *int, blocked *bool) dyngo.EventListener {
			return OnHTTPHandlerOperationStart(func(op dyngo.Operation, args HTTPHandlerArgs) {
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
			})
		}

		jsonBodyValueListener := func(called *int, value *interface{}) dyngo.EventListener {
			return OnHTTPHandlerOperationStart(func(op dyngo.Operation, _ HTTPHandlerArgs) {
				op.On(OnJSONParserOperationStart(func(op dyngo.Operation, v JSONParserArgs) {
					didBodyRead := false

					op.On(OnBodyReadOperationStart(func(_ dyngo.Operation, _ BodyReadArgs) {
						didBodyRead = true
					}))

					op.On(OnJSONParserOperationFinish(func(op dyngo.Operation, res JSONParserRes) {
						*called++
						if !didBodyRead || res.Err != nil {
							return
						}
						*value = res.Value
					}))
				}))
			})
		}

		t.Run("operation-stacking", func(t *testing.T) {
			// Run an operation stack that is monitored and not blocked by waf
			root := startOperation(RootArgs{}, nil)

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

			root.On(rawBodyListener)
			root.On(wafListener)
			root.On(jsonBodyValueListener)

			// Run the monitored stack of operations
			runOperation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerRes{},
				func(op dyngo.Operation) {
					runOperation(op, JSONParserArgs{}, JSONParserRes{Value: []interface{}{"a", "json", "array"}}, func(op dyngo.Operation) {
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("my ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("bo")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					runOperation(op, SQLQueryArgs{}, SQLQueryRes{}, nil)
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

		t.Run("operation-stacking", func(t *testing.T) {
			// Operation stack monitored and blocked by waf via the http operation monitoring
			root := startOperation(RootArgs{}, nil)

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

			root.On(rawBodyListener)
			root.On(wafListener)
			root.On(jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			runOperation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=attack"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerRes{},
				func(op dyngo.Operation) {
					runOperation(op, JSONParserArgs{}, JSONParserRes{Value: "a string", Err: errors.New("an error")}, func(op dyngo.Operation) {
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("another ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("bo")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("dy"), Err: nil}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte(" value"), Err: io.EOF}, nil)
					})

					runOperation(op, SQLQueryArgs{}, SQLQueryRes{}, nil)
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

		t.Run("operation-stack", func(t *testing.T) {
			// Operation stack not monitored
			root := startOperation(RootArgs{}, nil)

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

			root.On(rawBodyListener)
			root.On(wafListener)
			root.On(jsonBodyValueListener)

			// Run the monitored stack of operations
			runOperation(
				root,
				GRPCHandlerArgs{}, GRPCHandlerRes{},
				func(op dyngo.Operation) {
					runOperation(op, JSONParserArgs{}, JSONParserRes{Value: []interface{}{"a", "json", "array"}}, func(op dyngo.Operation) {
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("my ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("bo")}, nil)
						runOperation(op, BodyReadArgs{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					runOperation(op, SQLQueryArgs{}, SQLQueryRes{}, nil)
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

	t.Run("recursive-operation", func(t *testing.T) {
		root := startOperation(RootArgs{}, nil)
		defer root.Finish(RootRes{})

		called := 0
		root.On(OnHTTPHandlerOperationStart(func(dyngo.Operation, HTTPHandlerArgs) {
			called++
		}))

		runOperation(root, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o dyngo.Operation) {
			runOperation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o dyngo.Operation) {
				runOperation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o dyngo.Operation) {
					runOperation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o dyngo.Operation) {
						runOperation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(dyngo.Operation) {
						})
					})
				})
			})
		})

		require.Equal(t, 5, called)
	})

	t.Run("wrapped-operation-type-assertion", func(t *testing.T) {
		// dyngo's API should allow to retrieve the actual wrapper types: an
		// event listener should be called with the wrapped value.

		// Define `myop` so that it wraps a dyngo.Operation value so that it
		// implements dyngo.Operation interface and we can check the event
		// listeners get called with a value of type `myop`.
		type myop struct {
			dyngo.Operation
			// count the number of calls to check the test is working as expected
			called int
		}

		// Create a root operation to listen for a child `myop` operation.
		someRoot := dyngo.NewOperation(nil)
		dyngo.StartOperation(someRoot, RootArgs{})
		defer dyngo.FinishOperation(someRoot, RootRes{})
		// Register start and finish event listeners, and type-assert that the
		// passed operation has type `myop`.
		someRoot.On(OnMyOperationStart(func(op dyngo.Operation, _ MyOperationArgs) {
			v, ok := op.(*myop)
			assert.True(t, ok)
			v.called++
		}))
		someRoot.On(OnMyOperationFinish(func(op dyngo.Operation, _ MyOperationRes) {
			v, ok := op.(*myop)
			assert.True(t, ok)
			v.called++
		}))

		// Create a `myop` pointer value and start an operation with it.
		op := &myop{Operation: dyngo.NewOperation(someRoot)}
		dyngo.StartOperation(op, MyOperationArgs{})
		dyngo.FinishOperation(op, MyOperationRes{})
		require.Equal(t, 2, op.called)
	})

	t.Run("concurrency", func(t *testing.T) {
		// root is the shared operation having concurrent accesses in this test
		root := startOperation(RootArgs{}, nil)
		defer root.Finish(RootRes{})

		// Create nbGoroutines registering event listeners concurrently
		nbGoroutines := 1000
		// The concurrency is maximized by using start barriers to sync the goroutine launches
		var done, started, startBarrier sync.WaitGroup

		done.Add(nbGoroutines)
		started.Add(nbGoroutines)
		startBarrier.Add(1)

		var calls uint32
		for g := 0; g < nbGoroutines; g++ {
			// Start a goroutine that registers its event listeners to root.
			// This allows to test the thread-safety of the underlying list of listeners.
			go func() {
				started.Done()
				startBarrier.Wait()
				defer done.Done()
				root.On(OnMyOperationStart(func(dyngo.Operation, MyOperationArgs) { atomic.AddUint32(&calls, 1) }))
				root.On(OnMyOperationFinish(func(dyngo.Operation, MyOperationRes) { atomic.AddUint32(&calls, 1) }))
			}()
		}

		// Wait for all the goroutines to be started
		started.Wait()
		// Release the start barrier
		startBarrier.Done()
		// Wait for the goroutines to be done
		done.Wait()

		// Create nbGoroutines emitting events concurrently
		done.Add(nbGoroutines)
		started.Add(nbGoroutines)
		startBarrier.Add(1)
		for g := 0; g < nbGoroutines; g++ {
			// Start a goroutine that emits the events with a new operation. This allows to test the thread-safety of
			// while emitting events.
			go func() {
				started.Done()
				startBarrier.Wait()
				defer done.Done()
				op := startOperation(MyOperationArgs{}, root)
				defer dyngo.FinishOperation(op, MyOperationRes{})
			}()
		}

		// Wait for all the goroutines to be started
		started.Wait()
		// Release the start barrier
		startBarrier.Done()
		// Wait for the goroutines to be done
		done.Wait()

		// The number of calls should be equal to the expected number of events
		require.Equal(t, uint32(nbGoroutines*2*nbGoroutines), atomic.LoadUint32(&calls))
	})
}

func TestSwapRootOperation(t *testing.T) {
	var onStartCalled, onFinishCalled int

	root := dyngo.NewRootOperation()
	root.On(OnMyOperationStart(func(dyngo.Operation, MyOperationArgs) {
		onStartCalled++
	}))
	root.On(OnMyOperationFinish(func(dyngo.Operation, MyOperationRes) {
		onFinishCalled++
	}))

	dyngo.SwapRootOperation(root)
	runOperation(nil, MyOperationArgs{}, MyOperationRes{}, func(op dyngo.Operation) {})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onFinishCalled)

	dyngo.SwapRootOperation(dyngo.NewRootOperation())
	runOperation(nil, MyOperationArgs{}, MyOperationRes{}, func(op dyngo.Operation) {})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onFinishCalled)

	dyngo.SwapRootOperation(nil)
	runOperation(nil, MyOperationArgs{}, MyOperationRes{}, func(op dyngo.Operation) {})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onFinishCalled)

	dyngo.SwapRootOperation(root)
	runOperation(nil, MyOperationArgs{}, MyOperationRes{}, func(op dyngo.Operation) {})
	require.Equal(t, 2, onStartCalled)
	require.Equal(t, 2, onFinishCalled)
}

// Helper type wrapping a dyngo.Operation to provide some helper function and
// method helping to simplify the source code of the tests
type operation struct{ dyngo.Operation }

// Helper function to create an operation, wrap it and start it
func startOperation(args interface{}, parent dyngo.Operation) operation {
	op := operation{dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}

// Helper method to finish the operation
func (op operation) Finish(res interface{}) { dyngo.FinishOperation(op, res) }

// Helper function to run operations recursively.
func runOperation(parent dyngo.Operation, args, res interface{}, child func(dyngo.Operation)) {
	op := startOperation(args, parent)
	defer dyngo.FinishOperation(op, res)
	if child != nil {
		child(op)
	}
}

func TestOperationData(t *testing.T) {
	t.Run("data-transit", func(t *testing.T) {
		data := 0
		op := startOperation(MyOperationArgs{}, nil)
		op.OnData(dyngo.NewDataListener(func(data *int) {
			*data++
		}))
		for i := 0; i < 10; i++ {
			op.EmitData(&data)
		}
		op.Finish(MyOperationRes{})
		require.Equal(t, 10, data)
	})

	t.Run("bubble-up", func(t *testing.T) {
		listener := dyngo.NewDataListener(func(data *int) { *data++ })
		t.Run("single-listener", func(t *testing.T) {
			data := 0
			op1 := startOperation(MyOperationArgs{}, nil)
			op1.OnData(listener)
			op2 := startOperation(MyOperation2Args{}, op1)
			for i := 0; i < 10; i++ {
				op2.EmitData(&data)
			}
			op2.Finish(MyOperation2Res{})
			op1.Finish(MyOperationRes{})
			require.Equal(t, 10, data)
		})

		t.Run("double-listener", func(t *testing.T) {
			data := 0
			op1 := startOperation(MyOperationArgs{}, nil)
			op1.OnData(listener)
			op2 := startOperation(MyOperation2Args{}, op1)
			op2.OnData(listener)
			for i := 0; i < 10; i++ {
				op2.EmitData(&data)
			}
			op2.Finish(MyOperation2Res{})
			op1.Finish(MyOperationRes{})
			require.Equal(t, 20, data)
		})
	})
}

func TestOperationEvents(t *testing.T) {
	t.Run("start-event", func(t *testing.T) {
		op1 := startOperation(MyOperationArgs{}, nil)

		var called int
		op1.On(OnMyOperation2Start(func(dyngo.Operation, MyOperation2Args) {
			called++
		}))

		op2 := startOperation(MyOperation2Args{}, op1)
		op2.Finish(MyOperation2Res{})

		// Called once
		require.Equal(t, 1, called)

		op2 = startOperation(MyOperation2Args{}, op1)
		op2.Finish(MyOperation2Res{})

		// Called again
		require.Equal(t, 2, called)

		// Finish the operation so that it gets disabled and its listeners removed
		dyngo.FinishOperation(op1, MyOperationRes{})

		op2 = startOperation(MyOperation2Args{}, op1)
		op2.Finish(MyOperation2Res{})

		// No longer called
		require.Equal(t, 2, called)
	})

	t.Run("finish-event", func(t *testing.T) {
		op1 := startOperation(MyOperationArgs{}, nil)

		var called int
		op1.On(OnMyOperation2Finish(func(dyngo.Operation, MyOperation2Res) {
			called++
		}))

		op2 := startOperation(MyOperation2Args{}, op1)
		op2.Finish(MyOperation2Res{})
		// Called once
		require.Equal(t, 1, called)

		op2 = startOperation(MyOperation2Args{}, op1)
		op2.Finish(MyOperation2Res{})
		// Called again
		require.Equal(t, 2, called)

		op3 := startOperation(MyOperation3Args{}, op2)
		op3.Finish(MyOperation3Res{})
		// Not called
		require.Equal(t, 2, called)

		op2 = startOperation(MyOperation2Args{}, op3)
		op2.Finish(MyOperation2Res{})
		// Called again
		require.Equal(t, 3, called)

		// Finish the operation so that it gets disabled and its listeners removed
		op1.Finish(MyOperationRes{})

		op2 = startOperation(MyOperation2Args{}, op3)
		op2.Finish(MyOperation2Res{})
		// No longer called
		require.Equal(t, 3, called)

		op2 = startOperation(MyOperation2Args{}, op2)
		op2.Finish(MyOperation2Res{})
		// No longer called
		require.Equal(t, 3, called)
	})

	t.Run("disabled-operation-registration", func(t *testing.T) {
		var calls int
		registerTo := func(op dyngo.Operation) {
			op.On(OnMyOperation2Start(func(dyngo.Operation, MyOperation2Args) {
				calls++
			}))
			op.On(OnMyOperation2Finish(func(dyngo.Operation, MyOperation2Res) {
				calls++
			}))
		}

		// Start an operation and register event listeners to it.
		// This step allows to test the listeners are called when the operation is alive
		op := startOperation(MyOperationArgs{}, nil)
		registerTo(op)

		// Trigger the registered events
		op2 := startOperation(MyOperation2Args{}, op)
		op2.Finish(MyOperation2Res{})
		// We should have 4 calls
		require.Equal(t, 2, calls)

		// Finish the operation to disable it. Its event listeners should then be removed.
		op.Finish(MyOperationRes{})

		// Trigger the same events
		op2 = startOperation(MyOperation2Args{}, op)
		op2.Finish(MyOperation2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 2, calls)

		// Register again, but it shouldn't work because the operation is finished.
		registerTo(op)
		// Trigger the same events
		op2 = startOperation(MyOperation2Args{}, op)
		op2.Finish(MyOperation2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 2, calls)
	})

	t.Run("event-listener-panic", func(t *testing.T) {
		t.Run("start", func(t *testing.T) {
			op := startOperation(MyOperationArgs{}, nil)
			defer op.Finish(MyOperationRes{})

			// Panic on start
			calls := 0
			op.On(OnMyOperationStart(func(dyngo.Operation, MyOperationArgs) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			}))
			// Start the operation triggering the event: it should not panic
			require.NotPanics(t, func() {
				op := startOperation(MyOperationArgs{}, op)
				require.NotNil(t, op)
				defer op.Finish(MyOperationRes{})
				require.Equal(t, calls, 1)
			})
		})

		t.Run("finish", func(t *testing.T) {
			op := startOperation(MyOperationArgs{}, nil)
			defer op.Finish(MyOperationRes{})
			// Panic on finish
			calls := 0
			op.On(OnMyOperationFinish(func(dyngo.Operation, MyOperationRes) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			}))
			// Run the operation triggering the finish event: it should not panic
			require.NotPanics(t, func() {
				op := startOperation(MyOperationArgs{}, op)
				require.NotNil(t, op)
				op.Finish(MyOperationRes{})
				require.Equal(t, calls, 1)
			})
		})
	})
}

func BenchmarkEvents(b *testing.B) {
	b.Run("emitting", func(b *testing.B) {
		// Benchmark the emission of events according to the operation stack length
		for length := 1; length <= 64; length *= 2 {
			b.Run(fmt.Sprintf("stack=%d", length), func(b *testing.B) {
				root := startOperation(MyOperationArgs{}, nil)
				defer root.Finish(MyOperationRes{})

				op := root
				for i := 0; i < length-1; i++ {
					op = startOperation(MyOperationArgs{}, op)
					defer op.Finish(MyOperationRes{})
				}

				b.Run("start event", func(b *testing.B) {
					root.On(OnMyOperationStart(func(dyngo.Operation, MyOperationArgs) {}))

					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						startOperation(MyOperationArgs{}, op)
					}
				})

				b.Run("start + finish events", func(b *testing.B) {
					root.On(OnMyOperationFinish(func(dyngo.Operation, MyOperationRes) {}))

					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						leafOp := startOperation(MyOperationArgs{}, op)
						leafOp.Finish(MyOperationRes{})
					}
				})
			})
		}
	})

	b.Run("registering", func(b *testing.B) {
		op := startOperation(MyOperationArgs{}, nil)
		defer op.Finish(MyOperationRes{})

		b.Run("start event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				op.On(OnMyOperationStart(func(dyngo.Operation, MyOperationArgs) {}))
			}
		})

		b.Run("finish event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				op.On(OnMyOperationFinish(func(dyngo.Operation, MyOperationRes) {}))
			}
		})
	})
}

func BenchmarkGoAssumptions(b *testing.B) {
	type (
		testS0 struct{}
		testS1 struct{}
		testS2 struct{}
		testS3 struct{}
		testS4 struct{}
	)

	// Compare map lookup times according to their key type.
	// The selected implementation assumes using reflect.TypeOf(v).Name() doesn't allocate memory
	// and is as good as "regular" string keys, whereas the use of reflect.Type keys is slower due
	// to the underlying struct copy of the reflect struct type descriptor which has a lot of
	// fields copied involved in the key comparison.
	b.Run("map lookups", func(b *testing.B) {
		b.Run("string keys", func(b *testing.B) {
			m := map[string]int{}
			key := "server.request.address.%d"
			keys := make([]string, 5)
			for i := 0; i < len(keys); i++ {
				key := fmt.Sprintf(key, i)
				keys[i] = key
				m[key] = i
			}

			b.ResetTimer()
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				_ = m[keys[n%len(keys)]]
			}
		})

		getType := func(i int) reflect.Type {
			i = i % 5
			switch i {
			case 0:
				return reflect.TypeOf(testS0{})
			case 1:
				return reflect.TypeOf(testS1{})
			case 2:
				return reflect.TypeOf(testS2{})
			case 3:
				return reflect.TypeOf(testS3{})
			case 4:
				return reflect.TypeOf(testS4{})
			}
			panic("oops")
		}

		b.Run("reflect.Type name keys", func(b *testing.B) {
			m := map[string]int{}
			for i := 0; i < 5; i++ {
				m[getType(i).Name()] = i
			}

			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var k string
				switch n % 5 {
				case 0:
					k = reflect.TypeOf(testS0{}).Name()
				case 1:
					k = reflect.TypeOf(testS1{}).Name()
				case 2:
					k = reflect.TypeOf(testS2{}).Name()
				case 3:
					k = reflect.TypeOf(testS3{}).Name()
				case 4:
					k = reflect.TypeOf(testS4{}).Name()
				}
				_ = m[k]
			}
		})

		b.Run("reflect.Type keys", func(b *testing.B) {
			m := map[reflect.Type]int{}
			for i := 0; i < 5; i++ {
				m[getType(i)] = i
			}

			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var k reflect.Type
				switch n % 5 {
				case 0:
					k = reflect.TypeOf(testS0{})
				case 1:
					k = reflect.TypeOf(testS1{})
				case 2:
					k = reflect.TypeOf(testS2{})
				case 3:
					k = reflect.TypeOf(testS3{})
				case 4:
					k = reflect.TypeOf(testS4{})
				}
				_ = m[k]
			}
		})

		b.Run("custom type struct keys", func(b *testing.B) {
			type typeDesc struct {
				pkgPath, name string
			}
			m := map[typeDesc]int{}
			for i := 0; i < 5; i++ {
				typ := getType(i)
				m[typeDesc{typ.PkgPath(), typ.Name()}] = i
			}

			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var k reflect.Type
				switch n % 5 {
				case 0:
					k = reflect.TypeOf(testS0{})
				case 1:
					k = reflect.TypeOf(testS1{})
				case 2:
					k = reflect.TypeOf(testS2{})
				case 3:
					k = reflect.TypeOf(testS3{})
				case 4:
					k = reflect.TypeOf(testS4{})
				}
				_ = m[typeDesc{k.PkgPath(), k.Name()}]
			}
		})
	})
}
