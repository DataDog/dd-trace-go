// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

type (
	testOp1Args struct{}
	testOp1Res  struct{}

	testOp2Args struct{}
	testOp2Res  struct{}

	testOp3Args struct{}
	testOp3Res  struct{}
)

func init() {
	dyngo.RegisterOperation((*testOp1Args)(nil), (*testOp1Res)(nil))
	dyngo.RegisterOperation((*testOp2Args)(nil), (*testOp2Res)(nil))
	dyngo.RegisterOperation((*testOp3Args)(nil), (*testOp3Res)(nil))
}

func TestOperationEvents(t *testing.T) {
	t.Run("start event", func(t *testing.T) {
		op1 := dyngo.StartOperation(testOp1Args{})

		var called int
		op1.OnStart((*testOp2Args)(nil), func(_ *dyngo.Operation, _ interface{}) {
			called++
		})

		// Not called
		require.Equal(t, 0, called)

		op2 := dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})

		// Called once
		require.Equal(t, 1, called)

		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})

		// Called again
		require.Equal(t, 2, called)

		// Finish the operation so that it gets disabled and its listeners removed
		op1.Finish(testOp1Res{})

		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})

		// No longer called
		require.Equal(t, 2, called)
	})

	t.Run("finish event", func(t *testing.T) {
		op1 := dyngo.StartOperation(testOp1Args{})

		var called int
		op1.OnFinish((*testOp2Res)(nil), func(_ *dyngo.Operation, _ interface{}) {
			called++
		})

		op2 := dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})
		// Called once
		require.Equal(t, 1, called)

		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})
		// Called again
		require.Equal(t, 2, called)

		op3 := dyngo.StartOperation(testOp3Args{}, dyngo.WithParent(op2))
		op3.Finish(testOp3Res{})
		// Not called
		require.Equal(t, 2, called)

		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op3))
		op2.Finish(testOp2Res{})
		// Called again
		require.Equal(t, 3, called)

		// Finish the operation so that it gets disabled and its listeners removed
		op1.Finish(testOp1Res{})

		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op1))
		op2.Finish(testOp2Res{})
		// No longer called
		require.Equal(t, 3, called)
	})

	t.Run("registering to a disabled operation", func(t *testing.T) {
		var calls int
		registerTo := func(op *dyngo.Operation) {
			op.OnStart((*testOp2Args)(nil), func(*dyngo.Operation, interface{}) {
				calls++
			})
			op.OnFinish((*testOp2Res)(nil), func(*dyngo.Operation, interface{}) {
				calls++
			})
			op.Register(
				dyngo.OnStartEventListener((*testOp2Args)(nil), func(*dyngo.Operation, interface{}) {
					calls++
				}),
				dyngo.OnFinishEventListener((*testOp2Res)(nil), func(*dyngo.Operation, interface{}) {
					calls++
				}),
			)
		}

		// Start an operation and register event listeners to it.
		// This step allows to test the listeners are called when the operation is alive
		op := dyngo.StartOperation(testOp1Args{})
		registerTo(op)

		// Trigger the registered events
		op2 := dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op))
		op2.Finish(testOp2Res{})
		// We should have 4 calls
		require.Equal(t, 4, calls)

		// Finish the operation to disable it. Its event listeners should then be removed.
		op.Finish(testOp1Res{})

		// Trigger the same events
		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op))
		op2.Finish(testOp2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 4, calls)

		// Register again, but it shouldn't work because the operation is finished.
		registerTo(op)
		// Trigger the same events
		op2 = dyngo.StartOperation(testOp2Args{}, dyngo.WithParent(op))
		op2.Finish(testOp2Res{})
		// The number of calls should be unchanged
		require.Equal(t, 4, calls)
	})

	t.Run("event listener panic", func(t *testing.T) {
		t.Run("start", func(t *testing.T) {
			op := dyngo.StartOperation(MyOperationArgs{})
			defer op.Finish(MyOperationRes{})

			// Panic on start
			calls := 0
			op.OnStart((*MyOperationArgs)(nil), func(op *dyngo.Operation, v interface{}) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			})
			// Start the operation triggering the event: it should not panic
			require.NotPanics(t, func() {
				op := dyngo.StartOperation(MyOperationArgs{}, dyngo.WithParent(op))
				require.NotNil(t, op)
				defer op.Finish(MyOperationRes{})
				require.Equal(t, calls, 1)
			})
		})

		t.Run("finish", func(t *testing.T) {
			op := dyngo.StartOperation(MyOperationArgs{})
			defer op.Finish(MyOperationRes{})
			// Panic on finish
			calls := 0
			op.OnFinish((*MyOperationRes)(nil), func(op *dyngo.Operation, v interface{}) {
				// Call counter to check we actually call this listener
				calls++
				panic(errors.New("oops"))
			})
			// Run the operation triggering the finish event: it should not panic
			require.NotPanics(t, func() {
				op := dyngo.StartOperation(MyOperationArgs{}, dyngo.WithParent(op))
				require.NotNil(t, op)
				op.Finish(MyOperationRes{})
				require.Equal(t, calls, 1)
			})
		})
	})
}

func BenchmarkEvents(b *testing.B) {
	type (
		benchOpArgs struct{ s []byte }
		benchOpRes  struct{ s []byte }
	)

	dyngo.RegisterOperation((*benchOpArgs)(nil), (*benchOpRes)(nil))

	b.Run("emitting", func(b *testing.B) {
		// Benchmark the emission of events according to the operation stack length
		for length := 1; length <= 64; length *= 2 {
			b.Run(fmt.Sprintf("stack=%d", length), func(b *testing.B) {
				buf := make([]byte, 1024)
				rand.Read(buf)

				root := dyngo.StartOperation(benchOpArgs{})
				defer root.Finish(benchOpRes{})

				op := root
				for i := 0; i < length-1; i++ {
					op = dyngo.StartOperation(benchOpArgs{}, dyngo.WithParent(op))
					defer op.Finish(benchOpRes{})
				}

				b.Run("start event", func(b *testing.B) {
					unreg := root.Register(dyngo.OnStartEventListener((*benchOpArgs)(nil), func(*dyngo.Operation, interface{}) {}))
					defer unreg()
					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						dyngo.StartOperation(benchOpArgs{buf}, dyngo.WithParent(op))
					}
				})

				b.Run("start + finish events", func(b *testing.B) {
					unreg := root.Register(dyngo.OnFinishEventListener((*benchOpRes)(nil), func(*dyngo.Operation, interface{}) {}))
					defer unreg()
					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						leaf := dyngo.StartOperation(benchOpArgs{buf}, dyngo.WithParent(op))
						leaf.Finish(benchOpRes{buf})
					}
				})
			})
		}
	})

	b.Run("registering", func(b *testing.B) {
		op := dyngo.StartOperation(benchOpArgs{})
		defer op.Finish(benchOpRes{})

		b.Run("start event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				op.OnStart((*benchOpArgs)(nil), func(op *dyngo.Operation, v interface{}) {})
			}
		})

		b.Run("finish event", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				op.OnFinish((*benchOpRes)(nil), func(op *dyngo.Operation, v interface{}) {})
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
