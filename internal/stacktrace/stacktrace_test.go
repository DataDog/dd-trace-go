// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewStackTrace(t *testing.T) {
	stack := skipAndCapture(defaultCallerSkip, defaultMaxDepth, nil)
	if len(stack) == 0 {
		t.Error("stacktrace should not be empty")
	}
}

func TestStackTraceCurrentFrame(t *testing.T) {
	// Check last frame for the values of the current function
	stack := skipAndCapture(defaultCallerSkip, defaultMaxDepth, nil)
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
	return skipAndCapture(defaultCallerSkip, defaultMaxDepth, nil)
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

func recursive(i int) StackTrace {
	if i == 0 {
		return skipAndCapture(defaultCallerSkip, defaultMaxDepth, nil)
	}

	return recursive(i - 1)
}

func TestTruncatedStack(t *testing.T) {
	stack := recursive(defaultMaxDepth * 2)

	require.Equal(t, defaultMaxDepth, len(stack))

	lambdaFrame := stack[0]
	require.EqualValues(t, 0, lambdaFrame.Index)
	require.Contains(t, lambdaFrame.File, "stacktrace_test.go")
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", lambdaFrame.Namespace)
	require.Equal(t, "", lambdaFrame.ClassName)
	require.Equal(t, "recursive", lambdaFrame.Function)

	for i := 1; i < defaultMaxDepth-defaultTopFrameDepth; i++ {
		require.EqualValues(t, i, stack[i].Index)
		require.Equal(t, "recursive", stack[i].Function)
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
		{"main-receiver-templated-func", "test.(*toto).templatedFunc[...]", symbol{
			"test",
			"*toto",
			"templatedFunc[...]",
		}},
		{"main-templated-receiver", "test.(*toto[...]).func", symbol{
			"test",
			"*toto[...]",
			"func",
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

func recursiveBench(i int, depth int, b *testing.B) StackTrace {
	if i == 0 {
		b.StartTimer()
		stack := skipAndCapture(defaultCallerSkip, depth*2, nil)
		b.StopTimer()
		return stack
	}

	return recursiveBench(i-1, depth, b)
}

func BenchmarkCaptureStackTrace(b *testing.B) {
	for _, depth := range []int{10, 20, 50, 100, 200} {
		b.Run(fmt.Sprintf("%v", depth), func(b *testing.B) {
			defaultMaxDepth = depth * 2 // Making sure we are capturing the full stack
			for n := 0; n < b.N; n++ {
				runtime.KeepAlive(recursiveBench(depth, depth, b))
			}
		})
	}
}
