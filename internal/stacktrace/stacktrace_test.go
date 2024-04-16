// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"github.com/stretchr/testify/require"
	"runtime"
	"testing"
)

func TestNewStackTrace(t *testing.T) {
	stack := Capture()
	if len(stack) == 0 {
		t.Error("stacktrace should not be empty")
	}
}

func TestStackTraceCurrentFrame(t *testing.T) {
	// Check last frame for the values of the current function
	stack := Capture()
	require.GreaterOrEqual(t, len(stack), 3)

	frame := stack[0]
	require.EqualValues(t, 0, frame.Index)
	require.Contains(t, frame.File, "stacktrace_test.go")
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", frame.Namespace)
	require.Equal(t, "", frame.ClassName)
	require.Equal(t, "TestStackTraceCurrentFrame", frame.Function)
}

type Test struct{}

func (t *Test) Method() StackTrace {
	return Capture()
}

func TestStackMethodReceiver(t *testing.T) {
	test := &Test{}
	stack := test.Method()
	require.GreaterOrEqual(t, len(stack), 3)

	frame := stack[0]
	require.EqualValues(t, 0, frame.Index)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", frame.Namespace)
	require.Equal(t, "*Test", frame.ClassName)
	require.Equal(t, "Method", frame.Function)
	require.Contains(t, frame.File, "stacktrace_test.go")
}

func recursive[T any](i int, f func() T) T {
	if i == 0 {
		return f()
	}

	return recursive[T](i-1, f)
}

func TestTruncatedStack(t *testing.T) {
	stack := recursive(defaultMaxDepth*2, func() StackTrace {
		return Capture()
	})

	require.Equal(t, defaultMaxDepth, len(stack))

	lambdaFrame := stack[0]
	require.EqualValues(t, 0, lambdaFrame.Index)
	require.Contains(t, lambdaFrame.File, "stacktrace_test.go")
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", lambdaFrame.Namespace)
	require.Equal(t, "", lambdaFrame.ClassName)
	require.Equal(t, "TestTruncatedStack.func1", lambdaFrame.Function)

	for i := 1; i < defaultMaxDepth-defaultTopFrameDepth; i++ {
		require.EqualValues(t, i, stack[i].Index)
		require.Equal(t, "recursive[...]", stack[i].Function)
		require.Contains(t, stack[i].File, "stacktrace_test.go")
		require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", stack[i].Namespace)
		require.Equal(t, "", stack[i].ClassName)
	}

	// Make sure top frames indexes are at least sum of the bottom frames and the caller skip
	// (we don't know how many frames above us there is so we can't check the exact index)
	for i := defaultMaxDepth - defaultTopFrameDepth; i < defaultMaxDepth; i++ {
		require.GreaterOrEqual(t, int(stack[i].Index), 2*defaultMaxDepth-defaultTopFrameDepth+defaultCallerSkip)
	}

	// Make sure the last frame is runtime.goexit
	require.Equal(t, "goexit", stack[defaultMaxDepth-1].Function)
	require.Equal(t, "runtime", stack[defaultMaxDepth-1].Namespace)
	require.Equal(t, "", stack[defaultMaxDepth-1].ClassName)
}

func TestParseSymbol(t *testing.T) {
	for _, test := range []struct {
		name, symbol string
		expected     symbol
	}{
		{"method-receiver-pointer", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test).Method", symbol{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace",
			"*Test",
			"Method",
		}},
		{"method-receiver", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(Test).Method", symbol{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace",
			"Test",
			"Method",
		}},
		{"sample", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetPackageFromSymbol", symbol{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace",
			"",
			"TestGetPackageFromSymbol",
		}},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetPackageFromSymbol.func1", symbol{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace",
			"",
			"TestGetPackageFromSymbol.func1",
		}},
		{"stdlib-sample", "os/exec.NewCmd", symbol{
			"os/exec",
			"",
			"NewCmd",
		}},
		{"stdlib-receiver-lambda", "os/exec.(*Cmd).Run.func1", symbol{
			"os/exec",
			"*Cmd",
			"Run.func1",
		}},
		{"main", "test.main", symbol{
			"test",
			"",
			"main",
		}},
		{"main-method-receiver", "test.(*Test).Method", symbol{
			"test",
			"*Test",
			"Method",
		}},
		{"main-receiver-templated", "test.(*toto).templatedFunc[...]", symbol{
			"test",
			"*toto",
			"templatedFunc[...]",
		}},
		{"main-lambda", "test.main.func1", symbol{
			"test",
			"",
			"main.func1",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, parseSymbol(test.symbol))
		})
	}
}

func BenchmarkTakeStackTrace(b *testing.B) {
	for n := 0; n < b.N; n++ {
		runtime.KeepAlive(Capture())
	}
}
