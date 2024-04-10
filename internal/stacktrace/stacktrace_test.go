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
	stack := Take()
	if len(stack) == 0 {
		t.Error("stacktrace should not be empty")
	}
}

func TestStackTraceCurrentFrame(t *testing.T) {
	// Check last frame for the values of the current function
	stack := Take()
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
	return Take()
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
		return Take()
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

func TestGetPackageFromSymbol(t *testing.T) {
	for _, test := range []struct {
		name, symbol, expected string
	}{
		{"method-receiver", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test).Method", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace"},
		{"sample", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetPackageFromSymbol", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace"},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetPackageFromSymbol.func1", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace"},
		{"main", "test.main", "test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, getPackageFromSymbol(test.symbol))
		})
	}
}

func TestGetMethodReceiverFromSymbol(t *testing.T) {
	for _, test := range []struct {
		name, symbol, expected string
	}{
		{"method-receiver", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test).Method", "*Test"},
		{"method-receiver", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(Test).Method", "Test"},
		{"sample", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetMethodReceiverFromSymbol", ""},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetMethodReceiverFromSymbol.func1", ""},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(TestGetMethodReceiverFromSymbol).test", "TestGetMethodReceiverFromSymbol"},
		{"main", "main.main", ""},
		{"main", "main.(Test).toto", "Test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, getMethodReceiverFromSymbol(test.symbol))
		})
	}

}

func TestGetFunctionNameFromSymbol(t *testing.T) {
	for _, test := range []struct {
		name, symbol, expected string
	}{
		{"method-receiver", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test).Method", "Method"},
		{"sample", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetFunctionNameFromSymbol", "TestGetFunctionNameFromSymbol"},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetFunctionNameFromSymbol.func1", "TestGetFunctionNameFromSymbol.func1"},
		{"lambda", "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestGetFunctionNameFromSymbol.func2.func1", "TestGetFunctionNameFromSymbol.func2.func1"},
		{"lambda", "main.templatedFunc[...]", "templatedFunc[...]"},
		{"main", "test.main", "main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, getFunctionNameFromSymbol(test.symbol))
		})
	}
}

func BenchmarkTakeStackTrace(b *testing.B) {
	for n := 0; n < b.N; n++ {
		runtime.KeepAlive(Take())
	}
}
