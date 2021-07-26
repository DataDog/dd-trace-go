package dyngo_test

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/appsec/internal/dyngo"
	"github.com/stretchr/testify/require"
	"reflect"
	"testing"
	"unsafe"
)

type (
	testOp1Args struct{}
	testOp1Res  struct{}

	testOp2Args struct{}
	testOp2Res  struct{}

	testOp3Args struct{}
	testOp3Res  struct{}
)

func TestOperationEvents(t *testing.T) {
	t.Run("single operation without listeners", func(t *testing.T) {
		require.NotPanics(t, func() {
			op := dyngo.StartOperation(testOp1Args{})
			op.Finish(testOp1Res{})
		})

		require.NotPanics(t, func() {
			op := dyngo.StartOperation(nil)
			op.Finish(nil)
		})
	})

	t.Run("start event", func(t *testing.T) {
		op1 := dyngo.StartOperation(testOp1Args{})

		var called int
		op1.OnStart(func(op *dyngo.Operation, args testOp2Args) {
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
		op1.OnFinish(func(op *dyngo.Operation, args testOp2Res) {
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
}

func TestGoAssumptions(t *testing.T) {
	// Interface values have the same size
	t.Run("reflect.Type interface value size", func(t *testing.T) {
		require.Equal(t, unsafe.Sizeof(reflect.Type(nil)), unsafe.Sizeof(interface{}(nil)))
	})
}

func BenchmarkGoAssumptions(b *testing.B) {
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
	})
}

type (
	testS0 struct{}
	testS1 struct{}
	testS2 struct{}
	testS3 struct{}
	testS4 struct{}
)
