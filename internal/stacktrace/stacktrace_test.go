// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"fmt"
	"runtime"
	"strings"
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
	require.Equal(t, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", frame.Namespace)
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
	require.Equal(t, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", frame.Namespace)
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
	require.Equal(t, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", lambdaFrame.Namespace)
	require.Equal(t, "", lambdaFrame.ClassName)
	require.Equal(t, "recursive", lambdaFrame.Function)

	for i := 1; i < defaultMaxDepth-defaultTopFrameDepth; i++ {
		require.EqualValues(t, i, stack[i].Index)
		require.Equal(t, "recursive", stack[i].Function)
		require.Contains(t, stack[i].File, "stacktrace_test.go")
		require.Equal(t, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", stack[i].Namespace)
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
		{"method-receiver-pointer", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace.(*Test).Method", symbol{
			"github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
			"*Test",
			"Method",
		}},
		{"method-receiver", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace.(Test).Method", symbol{
			"github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
			"Test",
			"Method",
		}},
		{"sample", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace.TestGetPackageFromSymbol", symbol{
			"github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
			"",
			"TestGetPackageFromSymbol",
		}},
		{"lambda", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace.TestGetPackageFromSymbol.func1", symbol{
			"github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
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

// Tests for stack unwinding functionality

func TestUnwindRedactedStackFromPC_ValidPC(t *testing.T) {
	// Capture a valid PC from current stack
	var pcs [1]uintptr
	n := runtime.Callers(1, pcs[:])
	require.Greater(t, n, 0, "Should capture at least one PC")

	stack := UnwindStackFromPC(pcs[0], WithRedaction())
	result := Format(stack)
	require.NotEmpty(t, result, "Should return non-empty string for valid PC")

	// Should contain this test function (which should not be redacted as it's test code)
	require.Contains(t, result, "TestUnwindRedactedStackFromPC_ValidPC", "Should include test function name")
	require.Contains(t, result, "stacktrace_test.go", "Should include test file name")
}

func TestShouldRedactSymbol_DatadogFrames(t *testing.T) {
	opts := frameOptions{
		skipInternalFrames:      false,
		redactCustomerFrames:    true,
		internalPackagePrefixes: internalSymbolPrefixes,
	}
	iter := framesIterator{frameOpts: opts}

	tests := []struct {
		name     string
		function string
		expected bool
	}{
		{
			name:     "dd-trace-go v2",
			function: "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.StartSpan",
			expected: false, // Should NOT redact
		},
		{
			name:     "dd-trace-go v1",
			function: "gopkg.in/DataDog/dd-trace-go.v1/ddtrace.StartSpan",
			expected: false, // Should NOT redact
		},
		{
			name:     "datadog agent",
			function: "github.com/DataDog/datadog-agent/pkg/trace.Process",
			expected: false, // Should NOT redact
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym := parseSymbol(tt.function)
			result := iter.shouldRedactSymbol(sym)
			require.Equal(t, tt.expected, result, "Frame %s redaction should be %v", tt.function, tt.expected)
		})
	}
}

func TestClassifyFrameForRedaction_RuntimeFrames(t *testing.T) {
	tests := []struct {
		name     string
		function string
		expected frameType
	}{
		{
			name:     "fmt package",
			function: "fmt.Println",
			expected: frameTypeRuntime,
		},
		{
			name:     "runtime package",
			function: "runtime.main",
			expected: frameTypeRuntime,
		},
		{
			name:     "net/http package",
			function: "net/http.(*Server).Serve",
			expected: frameTypeRuntime,
		},
		{
			name:     "encoding/json",
			function: "encoding/json.Marshal",
			expected: frameTypeRuntime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifySymbol(parseSymbol(tt.function), internalSymbolPrefixes)
			require.Equal(t, tt.expected, result, "Frame %s should be classified as %s", tt.function, tt.expected)
		})
	}
}

func TestClassifyFrameForRedaction_ThirdPartyFrames(t *testing.T) {
	tests := []struct {
		name     string
		function string
		expected frameType
	}{
		{
			name:     "gin framework",
			function: "github.com/gin-gonic/gin.(*Engine).ServeHTTP",
			expected: frameTypeThirdParty,
		},
		{
			name:     "gorilla mux",
			function: "github.com/gorilla/mux.(*Router).ServeHTTP",
			expected: frameTypeThirdParty,
		},
		{
			name:     "redis client",
			function: "github.com/go-redis/redis.(*Client).Process",
			expected: frameTypeThirdParty,
		},
		{
			name:     "mongo driver",
			function: "go.mongodb.org/mongo-driver/mongo.Connect",
			expected: frameTypeThirdParty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifySymbol(parseSymbol(tt.function), internalSymbolPrefixes)
			require.Equal(t, tt.expected, result, "Frame %s should be classified as %s", tt.function, tt.expected)
		})
	}
}

func TestClassifyFrameForRedaction_CustomerFrames(t *testing.T) {
	tests := []struct {
		name     string
		function string
		expected frameType
	}{
		{
			name:     "main package",
			function: "main.main",
			expected: frameTypeCustomer,
		},
		{
			name:     "customer github repo",
			function: "github.com/customer/myapp/pkg.Function",
			expected: frameTypeCustomer,
		},
		{
			name:     "unknown third party",
			function: "example.com/unknownpkg.Function",
			expected: frameTypeCustomer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifySymbol(parseSymbol(tt.function), internalSymbolPrefixes)
			require.Equal(t, tt.expected, result, "Frame %s should be classified as %s", tt.function, tt.expected)
		})
	}
}

func TestIsStandardLibraryPackage(t *testing.T) {
	tests := []struct {
		name     string
		pkg      string
		expected bool
	}{
		// Standard library - single element packages
		{"fmt package", "fmt", true},
		{"os package", "os", true},
		{"runtime package", "runtime", true},

		// Standard library - multi-element packages
		{"net/http package", "net/http", true},
		{"encoding/json", "encoding/json", true},
		{"crypto/tls", "crypto/tls", true},

		// Standard library - special cases
		{"go tools", "go/ast", true},
		{"cmd tools", "cmd/link/internal/ld", true},

		// Non-standard library
		{"main package", "main", false},
		{"third-party github", "github.com/user/repo", false},
		{"third-party gopkg.in", "gopkg.in/yaml.v3", false},
		{"datadog repo", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isStandardLibraryPackage(tt.pkg)
			require.Equal(t, tt.expected, result, "isStandardLibraryPackage(%q) = %v, want %v", tt.pkg, result, tt.expected)
		})
	}
}

func TestUnwindRedactedStackFromPC_CustomerCodeRedaction(t *testing.T) {
	// This test is tricky because we need to simulate customer code
	// We'll check that the redaction mechanism works by looking at the output format

	var pcs [1]uintptr
	n := runtime.Callers(1, pcs[:])
	require.Greater(t, n, 0, "Should capture at least one PC")

	stack := UnwindStackFromPC(pcs[0], WithRedaction())
	result := Format(stack)
	require.NotEmpty(t, result, "Should return non-empty string")

	// The result should be formatted as "function\n\tfile:line"
	lines := strings.Split(result, "\n")
	require.GreaterOrEqual(t, len(lines), 2, "Should have at least function and file:line")

	// Check that we have the expected format
	for i := 0; i < len(lines); i += 2 {
		if i+1 < len(lines) {
			// Function line
			require.NotEmpty(t, lines[i], "Function line should not be empty")
			// File:line should start with tab
			require.True(t, strings.HasPrefix(lines[i+1], "\t"), "File:line should start with tab")
		}
	}
}

func TestUnwindRedactedStackFromPC_RedactionPlaceholders(t *testing.T) {
	// We can't easily create customer code frames in tests, but we can test
	// that the redaction constants and logic are properly set up

	// Test that redacted placeholder is defined
	require.Equal(t, "REDACTED", redactedPlaceholder, "Redacted placeholder should be 'REDACTED'")

	// Test frame types are defined
	require.Equal(t, frameType("datadog"), frameTypeDatadog)
	require.Equal(t, frameType("runtime"), frameTypeRuntime)
	require.Equal(t, frameType("third_party"), frameTypeThirdParty)
	require.Equal(t, frameType("customer"), frameTypeCustomer)
}

// Tests for new parametric functionality

func TestUnwindStackFromPC_WithOptions(t *testing.T) {
	var pcs [1]uintptr
	n := runtime.Callers(1, pcs[:])
	require.Greater(t, n, 0, "Should capture at least one PC")

	t.Run("default options", func(t *testing.T) {
		stack := UnwindStackFromPC(pcs[0])
		require.NotEmpty(t, stack, "Should return non-empty stack")

		// Default should include redaction
		formatted := Format(stack)
		require.Contains(t, formatted, "TestUnwindStackFromPC_WithOptions", "Should include test function")
	})

	t.Run("with redaction disabled", func(t *testing.T) {
		stack := UnwindStackFromPC(pcs[0], WithoutRedaction())
		require.NotEmpty(t, stack, "Should return non-empty stack")

		// Without redaction, all frames should have their original info
		for _, frame := range stack {
			require.NotEqual(t, redactedPlaceholder, frame.Function, "Function should not be redacted")
			require.NotEqual(t, redactedPlaceholder, frame.File, "File should not be redacted")
		}
	})

	t.Run("with custom max depth", func(t *testing.T) {
		maxDepth := 5
		stack := UnwindStackFromPC(pcs[0], WithMaxDepth(maxDepth))
		t.Logf("Requested maxDepth: %d, got stack length: %d", maxDepth, len(stack))
		for i, frame := range stack {
			t.Logf("Frame %d: %s.%s at %s:%d", i, frame.Namespace, frame.Function, frame.File, frame.Line)
		}
		require.LessOrEqual(t, len(stack), maxDepth, "Stack should respect max depth limit")
	})

	t.Run("with skip frames", func(t *testing.T) {
		skipFrames := 2
		stackNoSkip := UnwindStackFromPC(pcs[0])
		stackWithSkip := UnwindStackFromPC(pcs[0], WithSkipFrames(skipFrames))

		require.NotEmpty(t, stackNoSkip, "Base stack should not be empty")
		require.NotEmpty(t, stackWithSkip, "Stack with skip should not be empty")
		// With skip frames, we should have fewer frames
		require.Less(t, len(stackWithSkip), len(stackNoSkip), "Skipped stack should have fewer frames")
	})

	t.Run("with internal frames disabled", func(t *testing.T) {
		stack := UnwindStackFromPC(pcs[0], WithInternalFrames(false))
		require.NotEmpty(t, stack, "Should return non-empty stack")

		// Check that no internal DD frames are present
		for _, frame := range stack {
			fullFunc := frame.Namespace + "." + frame.Function
			for _, prefix := range internalSymbolPrefixes {
				require.False(t, strings.Contains(fullFunc, prefix),
					"Should not contain internal frame: %s", fullFunc)
			}
		}
	})
}

func TestFormat(t *testing.T) {
	t.Run("empty stack", func(t *testing.T) {
		result := Format(nil)
		require.Empty(t, result, "Empty stack should return empty string")
	})

	t.Run("single frame", func(t *testing.T) {
		stack := StackTrace{
			{
				Function:  "TestFunction",
				File:      "/path/to/file.go",
				Line:      42,
				Namespace: "github.com/example/pkg",
				ClassName: "",
			},
		}

		result := Format(stack)
		expected := "github.com/example/pkg.TestFunction\n\t/path/to/file.go:42"
		require.Equal(t, expected, result)
	})

	t.Run("frame with class", func(t *testing.T) {
		stack := StackTrace{
			{
				Function:  "Method",
				File:      "/path/to/file.go",
				Line:      100,
				Namespace: "github.com/example/pkg",
				ClassName: "*MyStruct",
			},
		}

		result := Format(stack)
		expected := "github.com/example/pkg.(*MyStruct).Method\n\t/path/to/file.go:100"
		require.Equal(t, expected, result)
	})

	t.Run("redacted frame", func(t *testing.T) {
		stack := StackTrace{
			{
				Function:  redactedPlaceholder,
				File:      redactedPlaceholder,
				Line:      0,
				Namespace: "",
				ClassName: "",
			},
		}

		result := Format(stack)
		expected := "REDACTED\n\tREDACTED:0"
		require.Equal(t, expected, result)
	})
}
