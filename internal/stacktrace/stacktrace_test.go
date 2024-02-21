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

	frame := stack[len(stack)-1]
	require.Equal(t, uint32(len(stack)-1), frame.Index)
	require.Contains(t, frame.File, "internal/stacktrace/stacktrace_test.go")
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", frame.Namespace)
	require.Equal(t, "", frame.ClassName)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestStackTraceCurrentFrame", frame.Function)
}

type Test struct{}

func (t *Test) Method() StackTrace {
	return Take()
}

func TestStackMethodReceiver(t *testing.T) {
	test := &Test{}
	stack := test.Method()
	require.GreaterOrEqual(t, len(stack), 3)

	frame := stack[len(stack)-1]
	require.Equal(t, uint32(len(stack)-1), frame.Index)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace", frame.Namespace)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test)", frame.ClassName)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Test).Method", frame.Function)
	require.Contains(t, frame.File, "internal/stacktrace/stacktrace_test.go")
}

func BenchmarkTakeStackTrace(b *testing.B) {
	for n := 0; n < b.N; n++ {
		runtime.KeepAlive(Take())
	}
}
