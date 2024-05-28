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

	"github.com/stretchr/testify/require"
)

// Dummy struct to mimic real-life operation stacks.
type (
	RootArgs struct{}
	RootRes  struct{}
)

func (RootArgs) IsArgOf(operation)   {}
func (RootRes) IsResultOf(operation) {}

type (
	HTTPHandlerArgs struct {
		URL     *url.URL
		Headers http.Header
	}
	HTTPHandlerRes struct{}
)

func (HTTPHandlerArgs) IsArgOf(operation)   {}
func (HTTPHandlerRes) IsResultOf(operation) {}

type (
	SQLQueryArgs struct {
		Query string
	}
	SQLQueryRes struct {
		Err error
	}
)

func (SQLQueryArgs) IsArgOf(operation)   {}
func (SQLQueryRes) IsResultOf(operation) {}

type (
	GRPCHandlerArgs struct {
		Msg interface{}
	}
	GRPCHandlerRes struct {
		Res interface{}
	}
)

func (GRPCHandlerArgs) IsArgOf(operation)   {}
func (GRPCHandlerRes) IsResultOf(operation) {}

type (
	JSONParserArgs struct {
		Buf []byte
	}
	JSONParserRes struct {
		Value interface{}
		Err   error
	}
)

func (JSONParserArgs) IsArgOf(operation)   {}
func (JSONParserRes) IsResultOf(operation) {}

type (
	BodyReadArgs struct{}
	BodyReadRes  struct {
		Buf []byte
		Err error
	}
)

func (BodyReadArgs) IsArgOf(operation)   {}
func (BodyReadRes) IsResultOf(operation) {}

type (
	MyOperationArgs struct{ n int }
	MyOperationRes  struct{ n int }
)

func (MyOperationArgs) IsArgOf(operation)   {}
func (MyOperationRes) IsResultOf(operation) {}

type (
	MyOperation2Args struct{}
	MyOperation2Res  struct{}
)

func (MyOperation2Args) IsArgOf(operation)   {}
func (MyOperation2Res) IsResultOf(operation) {}

type (
	MyOperation3Args struct{}
	MyOperation3Res  struct{}
)

func (MyOperation3Args) IsArgOf(operation)   {}
func (MyOperation3Res) IsResultOf(operation) {}

func TestUsage(t *testing.T) {
	t.Run("operation-stacking", func(t *testing.T) {
		// HTTP body read listener appending the read results to a buffer
		rawBodyListener := func(called *int, buf *[]byte) dyngo.EventListener[operation, HTTPHandlerArgs] {
			return func(op operation, _ HTTPHandlerArgs) {
				dyngo.OnFinish(op, func(op operation, res BodyReadRes) {
					*called++
					*buf = append(*buf, res.Buf...)
				})
			}
		}

		// Dummy waf looking for the string `attack` in HTTPHandlerArgs
		wafListener := func(called *int, blocked *bool) dyngo.EventListener[operation, HTTPHandlerArgs] {
			return func(op operation, args HTTPHandlerArgs) {
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
			}
		}

		jsonBodyValueListener := func(called *int, value *interface{}) dyngo.EventListener[operation, HTTPHandlerArgs] {
			return func(op operation, _ HTTPHandlerArgs) {
				dyngo.On(op, func(op operation, v JSONParserArgs) {
					didBodyRead := false

					dyngo.On(op, func(_ operation, _ BodyReadArgs) {
						didBodyRead = true
					})

					dyngo.OnFinish(op, func(op operation, res JSONParserRes) {
						*called++
						if !didBodyRead || res.Err != nil {
							return
						}
						*value = res.Value
					})
				})
			}
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

			dyngo.On(root, rawBodyListener)
			dyngo.On(root, wafListener)
			dyngo.On(root, jsonBodyValueListener)

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

			dyngo.On(root, rawBodyListener)
			dyngo.On(root, wafListener)
			dyngo.On(root, jsonBodyValueListener)

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

			dyngo.On(root, rawBodyListener)
			dyngo.On(root, wafListener)
			dyngo.On(root, jsonBodyValueListener)

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
		defer dyngo.FinishOperation(root, RootRes{})

		called := 0
		dyngo.On(root, func(operation, HTTPHandlerArgs) { called++ })

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

	t.Run("concurrency", func(t *testing.T) {
		// root is the shared operation having concurrent accesses in this test
		root := startOperation(RootArgs{}, nil)
		defer dyngo.FinishOperation(root, RootRes{})

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
				dyngo.On(root, func(operation, MyOperationArgs) { atomic.AddUint32(&calls, 1) })
				dyngo.OnFinish(root, func(operation, MyOperationRes) { atomic.AddUint32(&calls, 1) })
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
	dyngo.On(root, func(operation, MyOperationArgs) { onStartCalled++ })
	dyngo.OnFinish(root, func(operation, MyOperationRes) { onFinishCalled++ })

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
func startOperation[T dyngo.ArgOf[operation]](args T, parent dyngo.Operation) operation {
	op := operation{dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}

// Helper function to run operations recursively.
func runOperation[A dyngo.ArgOf[operation], R dyngo.ResultOf[operation]](parent dyngo.Operation, args A, res R, child func(dyngo.Operation)) {
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
		dyngo.OnData(op, func(data *int) {
			*data++
		})
		for i := 0; i < 10; i++ {
			dyngo.EmitData(op, &data)
		}
		dyngo.FinishOperation(op, MyOperationRes{})
		require.Equal(t, 10, data)
	})

	t.Run("bubble-up", func(t *testing.T) {
		listener := func(data *int) { *data++ }
		t.Run("single-listener", func(t *testing.T) {
			data := 0
			op1 := startOperation(MyOperationArgs{}, nil)
			dyngo.OnData(op1, listener)
			op2 := startOperation(MyOperation2Args{}, op1)
			for i := 0; i < 10; i++ {
				dyngo.EmitData(op2, &data)
			}
			dyngo.FinishOperation(op2, MyOperation2Res{})
			dyngo.FinishOperation(op1, MyOperationRes{})
			require.Equal(t, 10, data)
		})

		t.Run("double-listener", func(t *testing.T) {
			data := 0
			op1 := startOperation(MyOperationArgs{}, nil)
			dyngo.OnData(op1, listener)
			op2 := startOperation(MyOperation2Args{}, op1)
			dyngo.OnData(op2, listener)
			for i := 0; i < 10; i++ {
				dyngo.EmitData(op2, &data)
			}
			dyngo.FinishOperation(op2, MyOperation2Res{})
			dyngo.FinishOperation(op1, MyOperationRes{})
			require.Equal(t, 20, data)
		})
	})
}

func TestOperationEvents(t *testing.T) {
	t.Run("start-event", func(t *testing.T) {
		op1 := startOperation(MyOperationArgs{}, nil)

		var called int
		dyngo.On(op1, func(operation, MyOperation2Args) {
			called++
		})

		op2 := startOperation(MyOperation2Args{}, op1)
		dyngo.FinishOperation(op2, MyOperation2Res{})

		// Called once
		require.Equal(t, 1, called)

		op2 = startOperation(MyOperation2Args{}, op1)
		dyngo.FinishOperation(op2, MyOperation2Res{})

		// Called again
		require.Equal(t, 2, called)

		// Finish the operation so that it gets disabled and its listeners removed
		dyngo.FinishOperation(op1, MyOperationRes{})

		op2 = startOperation(MyOperation2Args{}, op1)
		dyngo.FinishOperation(op2, MyOperation2Res{})

		// No longer called
		require.Equal(t, 2, called)
	})

	t.Run("finish-event", func(t *testing.T) {
		op1 := startOperation(MyOperationArgs{}, nil)

		var called int
		dyngo.OnFinish(op1, func(operation, MyOperation2Res) {
			called++
		})

		op2 := startOperation(MyOperation2Args{}, op1)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// Called once
		require.Equal(t, 1, called)

		op2 = startOperation(MyOperation2Args{}, op1)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// Called again
		require.Equal(t, 2, called)

		op3 := startOperation(MyOperation3Args{}, op2)
		dyngo.FinishOperation(op3, MyOperation3Res{})
		// Not called
		require.Equal(t, 2, called)

		op2 = startOperation(MyOperation2Args{}, op3)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// Called again
		require.Equal(t, 3, called)

		// Finish the operation so that it gets disabled and its listeners removed
		dyngo.FinishOperation(op1, MyOperationRes{})

		op2 = startOperation(MyOperation2Args{}, op3)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// No longer called
		require.Equal(t, 3, called)

		op2 = startOperation(MyOperation2Args{}, op2)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// No longer called
		require.Equal(t, 3, called)
	})

	t.Run("disabled-operation-registration", func(t *testing.T) {
		var calls int
		registerTo := func(op dyngo.Operation) {
			dyngo.On(op, func(operation, MyOperation2Args) {
				calls++
			})
			dyngo.OnFinish(op, func(operation, MyOperation2Res) {
				calls++
			})
		}

		// Start an operation and register event listeners to it.
		// This step allows to test the listeners are called when the operation is alive
		op := startOperation(MyOperationArgs{}, nil)
		registerTo(op)

		// Trigger the registered events
		op2 := startOperation(MyOperation2Args{}, op)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// We should have 4 calls
		require.Equal(t, 2, calls)

		// Finish the operation to disable it. Its event listeners should then be removed.
		dyngo.FinishOperation(op, MyOperationRes{})

		// Trigger the same events
		op2 = startOperation(MyOperation2Args{}, op)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 2, calls)

		// Register again, but it shouldn't work because the operation is finished.
		registerTo(op)
		// Trigger the same events
		op2 = startOperation(MyOperation2Args{}, op)
		dyngo.FinishOperation(op2, MyOperation2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 2, calls)
	})

	t.Run("event-listener-panic", func(t *testing.T) {
		t.Run("start", func(t *testing.T) {
			op := startOperation(MyOperationArgs{}, nil)
			defer dyngo.FinishOperation(op, MyOperationRes{})

			// Panic on start
			calls := 0
			dyngo.On(op, func(operation, MyOperationArgs) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			})
			// Start the operation triggering the event: it should not panic
			require.NotPanics(t, func() {
				op := startOperation(MyOperationArgs{}, op)
				require.NotNil(t, op)
				defer dyngo.FinishOperation(op, MyOperationRes{})
				require.Equal(t, calls, 1)
			})
		})

		t.Run("finish", func(t *testing.T) {
			op := startOperation(MyOperationArgs{}, nil)
			defer dyngo.FinishOperation(op, MyOperationRes{})
			// Panic on finish
			calls := 0
			dyngo.OnFinish(op, func(operation, MyOperationRes) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			})
			// Run the operation triggering the finish event: it should not panic
			require.NotPanics(t, func() {
				op := startOperation(MyOperationArgs{}, op)
				require.NotNil(t, op)
				dyngo.FinishOperation(op, MyOperationRes{})
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
				defer dyngo.FinishOperation(root, MyOperationRes{})

				op := root
				for i := 0; i < length-1; i++ {
					op = startOperation(MyOperationArgs{}, op)
					defer dyngo.FinishOperation(op, MyOperationRes{})
				}

				b.Run("start event", func(b *testing.B) {
					dyngo.On(root, func(operation, MyOperationArgs) {})

					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						startOperation(MyOperationArgs{}, op)
					}
				})

				b.Run("start + finish events", func(b *testing.B) {
					dyngo.OnFinish(root, func(operation, MyOperationRes) {})

					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						leafOp := startOperation(MyOperationArgs{}, op)
						dyngo.FinishOperation(leafOp, MyOperationRes{})
					}
				})
			})
		}
	})

	b.Run("registering", func(b *testing.B) {
		op := startOperation(MyOperationArgs{}, nil)
		defer dyngo.FinishOperation(op, MyOperationRes{})

		b.Run("start event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				dyngo.On(op, func(operation, MyOperationArgs) {})
			}
		})

		b.Run("finish event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				dyngo.OnFinish(op, func(operation, MyOperationRes) {})
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
