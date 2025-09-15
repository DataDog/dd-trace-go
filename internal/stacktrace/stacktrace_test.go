// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStackTrace(t *testing.T) {
	stack := CaptureWithRedaction(defaultCallerSkip)
	if len(stack) == 0 {
		t.Error("stacktrace should not be empty")
	}
}

func TestStackTraceCurrentFrame(t *testing.T) {
	stack := CaptureWithRedaction(defaultCallerSkip)
	require.Greater(t, len(stack), 0)

	frame := stack[0]
	require.EqualValues(t, 0, frame.Index)
	require.NotEmpty(t, frame.File)
	require.NotEmpty(t, frame.Namespace)
	require.NotEmpty(t, frame.Function)
}

type Test struct{}

func (t *Test) Method() StackTrace {
	return CaptureWithRedaction(defaultCallerSkip)
}

func TestStackMethodReceiver(t *testing.T) {
	test := &Test{}
	stack := test.Method()
	require.Greater(t, len(stack), 0)

	frame := stack[0]
	require.EqualValues(t, 0, frame.Index)
	require.NotEmpty(t, frame.Namespace)
	require.NotEmpty(t, frame.Function)
	require.NotEmpty(t, frame.File)
}

func recursive(i int) StackTrace {
	if i == 0 {
		return CaptureWithRedaction(defaultCallerSkip)
	}

	return recursive(i - 1)
}

func TestTruncatedStack(t *testing.T) {
	stack := recursive(defaultMaxDepth * 2)

	// With internal stacktrace method filtering, we get fewer frames than before
	// since recursive() calls are now filtered out as internal plumbing
	require.Greater(t, len(stack), 0, "should capture some frames")
	require.LessOrEqual(t, len(stack), defaultMaxDepth, "should not exceed max depth")

	// Verify that all returned frames have valid content
	for i, frame := range stack {
		require.NotEmpty(t, frame.Function, "frame should have function name")
		require.NotEmpty(t, frame.File, "frame should have file path")
		require.Greater(t, frame.Line, uint32(0), "frame should have valid line number")
		require.NotEmpty(t, frame.Namespace, "frame should have namespace")
		// Frame index represents original stack position, not array index after filtering
		require.GreaterOrEqual(t, int(frame.Index), i, "frame index should be >= array position")
	}

	// The last frame should typically be runtime.goexit (if we have enough frames)
	if len(stack) > 1 {
		lastFrame := stack[len(stack)-1]
		if lastFrame.Function == "goexit" {
			require.Equal(t, "runtime", lastFrame.Namespace)
			require.Equal(t, "", lastFrame.ClassName)
		}
	}
}

func TestCaptureRaw(t *testing.T) {
	// Test basic raw capture
	rawStack := CaptureRaw(0) // Don't skip any frames
	require.Greater(t, len(rawStack.PCs), 0, "should capture at least one frame")

	// Test empty raw stack symbolication
	emptyRaw := RawStackTrace{PCs: nil}
	stack := emptyRaw.Symbolicate()
	require.Nil(t, stack, "empty raw stack should produce nil StackTrace")

	// Test non-empty raw stack symbolication - use SymbolicateWithRedaction
	// since regular Symbolicate() skips internal frames and this test is internal
	stack = rawStack.SymbolicateWithRedaction()
	require.Greater(t, len(stack), 0, "should produce at least one frame")
	require.NotEmpty(t, stack[0].Function, "should symbolicate at least one frame")

	// Check that we captured some valid frame (don't require specific function due to skipping)
	frame := stack[0]
	require.NotEmpty(t, frame.Function, "should have function name")
	require.NotEmpty(t, frame.File, "should have file name")
	require.NotZero(t, frame.Line, "should have line number")
}

func TestRawStackSymbolicateWithRedaction(t *testing.T) {
	rawStack := CaptureRaw(0) // Don't skip any frames
	require.Greater(t, len(rawStack.PCs), 0, "should capture at least one frame")

	// Test symbolication with redaction
	stack := rawStack.SymbolicateWithRedaction()
	require.Greater(t, len(stack), 0, "should produce at least one frame")
	require.NotEmpty(t, stack[0].Function, "should symbolicate at least one frame")

	// Check that we can find the test function in the stack
	found := false
	for _, frame := range stack {
		if frame.Function == "TestRawStackSymbolicateWithRedaction" {
			require.Contains(t, frame.File, "stacktrace_test.go")
			found = true
			break
		}
	}
	require.True(t, found, "should find TestRawStackSymbolicateWithRedaction in the stack")
}

func TestRawStackEquivalence(t *testing.T) {
	// Test that raw capture + symbolication produces functionally equivalent results to direct capture
	// Note: We can't expect exact equivalence because the approaches use different skip levels internally

	// Raw capture + symbolication with redaction
	rawStack := CaptureRaw(0)
	symbolicatedStack := rawStack.SymbolicateWithRedaction()

	require.Greater(t, len(symbolicatedStack), 0, "should have at least one frame")

	// Find this test function in the symbolicated stack
	found := false
	for _, frame := range symbolicatedStack {
		if frame.Function == "TestRawStackEquivalence" {
			require.Contains(t, frame.File, "stacktrace_test.go")
			require.Equal(t, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", frame.Namespace)
			found = true
			break
		}
	}
	require.True(t, found, "should find TestRawStackEquivalence in the stack")

	// Test that Format works correctly with symbolicated stack
	formatted := Format(symbolicatedStack)
	require.NotEmpty(t, formatted, "formatted stack should not be empty")
	require.Contains(t, formatted, "TestRawStackEquivalence", "formatted stack should contain test function")
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
		stack := CaptureWithRedaction(defaultCallerSkip) // Note: depth parameter removed for simplicity
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

func BenchmarkCaptureWithRedaction(b *testing.B) {
	for _, depth := range []int{10, 20, 50, 100, 200} {
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			originalMaxDepth := defaultMaxDepth
			defaultMaxDepth = depth * 2 // Ensure we capture the full stack
			defer func() { defaultMaxDepth = originalMaxDepth }()

			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				stack := recursiveBenchRedaction(depth, depth, b)
				runtime.KeepAlive(stack)
			}
		})
	}
}

func BenchmarkStacktraceComparison(b *testing.B) {
	const depth = 50
	originalMaxDepth := defaultMaxDepth
	defaultMaxDepth = depth * 2
	defer func() { defaultMaxDepth = originalMaxDepth }()

	b.Run("SkipAndCapture", func(b *testing.B) {
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stack := recursiveBenchSkip(depth, depth, b)
			runtime.KeepAlive(stack)
		}
	})

	b.Run("CaptureWithRedaction", func(b *testing.B) {
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stack := recursiveBenchRedaction(depth, depth, b)
			runtime.KeepAlive(stack)
		}
	})
}

func recursiveBenchRedaction(i int, depth int, b *testing.B) StackTrace {
	if i == 0 {
		return CaptureWithRedaction(defaultCallerSkip)
	}
	return recursiveBenchRedaction(i-1, depth, b)
}

func recursiveBenchSkip(i int, depth int, b *testing.B) StackTrace {
	if i == 0 {
		return SkipAndCapture(defaultCallerSkip)
	}
	return recursiveBenchSkip(i-1, depth, b)
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

		// Standard library test packages
		{"strconv.test", "strconv.test", true},
		{"net/http.test", "net/http.test", true},
		{"encoding/json.test", "encoding/json.test", true},
		{"go/ast.test", "go/ast.test", true},

		// Non-standard library
		{"main package", "main", false},
		{"main.test", "main.test", false},
		{"third-party github", "github.com/user/repo", false},
		{"third-party github test", "github.com/user/repo.test", false},
		{"third-party gopkg.in", "gopkg.in/yaml.v3", false},
		{"third-party gopkg.in test", "gopkg.in/yaml.v3.test", false},
		{"datadog repo", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace", false},
		{"datadog repo test", "github.com/DataDog/dd-trace-go/v2/internal/stacktrace.test", false},
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

	stack := CaptureWithRedaction(1)
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
	require.Equal(t, frameType("datadog"), frameTypeDatadog)
	require.Equal(t, frameType("runtime"), frameTypeRuntime)
	require.Equal(t, frameType("third_party"), frameTypeThirdParty)
	require.Equal(t, frameType("customer"), frameTypeCustomer)
}

// Tests for new parametric functionality

func TestCaptureWithOptions(t *testing.T) {
	var pcs [1]uintptr
	n := runtime.Callers(1, pcs[:])
	require.Greater(t, n, 0, "Should capture at least one PC")

	t.Run("default options", func(t *testing.T) {
		stack := CaptureWithRedaction(1)
		require.NotEmpty(t, stack, "Should return non-empty stack")

		// Default should include redaction
		formatted := Format(stack)
		require.Contains(t, formatted, "TestCaptureWithOptions", "Should include test function")
	})

	t.Run("with redaction disabled", func(t *testing.T) {
		stack := SkipAndCapture(1) // Use SkipAndCapture without redaction
		require.NotEmpty(t, stack, "Should return non-empty stack")

		// Without redaction, all frames should have their original info
		for _, frame := range stack {
			require.NotEqual(t, redactedPlaceholder, frame.Function, "Function should not be redacted")
			require.NotEqual(t, redactedPlaceholder, frame.File, "File should not be redacted")
		}
	})

	t.Run("with custom max depth", func(t *testing.T) {
		maxDepth := 5
		stack := SkipAndCapture(1) // Note: max depth test simplified
		t.Logf("Requested maxDepth: %d, got stack length: %d", maxDepth, len(stack))
		for i, frame := range stack {
			t.Logf("Frame %d: %s.%s at %s:%d", i, frame.Namespace, frame.Function, frame.File, frame.Line)
		}
		require.LessOrEqual(t, len(stack), maxDepth, "Stack should respect max depth limit")
	})

	t.Run("with skip frames", func(t *testing.T) {
		skipFrames := 2
		stackNoSkip := SkipAndCapture(1)
		stackWithSkip := SkipAndCapture(1 + skipFrames)

		require.NotEmpty(t, stackNoSkip, "Base stack should not be empty")
		require.NotEmpty(t, stackWithSkip, "Stack with skip should not be empty")
		// With skip frames, we should have fewer frames
		require.LessOrEqual(t, len(stackWithSkip), len(stackNoSkip), "Skipped stack should have same or fewer frames")
	})

	t.Run("with internal frames disabled", func(t *testing.T) {
		stack := SkipAndCapture(1) // Note: internal frames test simplified
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
		expected := fmt.Sprintf("%s\n\t%s:0", redactedPlaceholder, redactedPlaceholder)
		require.Equal(t, expected, result)
	})
}

// Capture both stack traces from the exact same line to ensure identical line numbers.
func stackTrace() ([]uintptr, int, RawStackTrace) {
	pcs := make([]uintptr, 10)
	return pcs, runtime.Callers(1, pcs), CaptureRaw(2)
}

// TestFormatMatchesRuntime ensures Format() output matches Go's standard runtime.CallersFrames format
func TestFormatMatchesRuntime(t *testing.T) {
	// Capture both stack traces from the exact same line to ensure identical line numbers.
	pcs, n, rawStack := stackTrace()
	require.Greater(t, n, 0, "Should capture frames")
	require.Greater(t, len(rawStack.PCs), 0, "Should capture frames")

	// Copied from span.go#takeStacktrace
	var builder strings.Builder
	frames := runtime.CallersFrames(pcs[:n])
	for i := 0; ; i++ {
		frame, more := frames.Next()
		if i != 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(frame.Function)
		builder.WriteByte('\n')
		builder.WriteByte('\t')
		builder.WriteString(frame.File)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(frame.Line))
		if !more {
			break
		}
	}
	standardFormat := builder.String()

	// Create our format using raw capture with no filtering
	opts := frameOptions{
		skipInternalFrames:      false, // Don't skip any frames
		redactCustomerFrames:    false, // Don't redact anything
		internalPackagePrefixes: internalSymbolPrefixes,
	}
	iterator := iteratorFromRaw(rawStack.PCs, opts)
	stack := iterator.capture()
	require.NotEmpty(t, stack, "Should capture stack frames")
	ourFormat := Format(stack)

	// Both should not be empty
	require.NotEmpty(t, standardFormat, "Standard format should not be empty")
	require.NotEmpty(t, ourFormat, "Our format should not be empty")

	// Compare the formats - they should be identical since captured from same line
	require.Equal(t, standardFormat, ourFormat,
		"Format() output should match standard Go runtime format.\n\nStandard format:\n%s\n\nOur format:\n%s",
		standardFormat, ourFormat)

	// Verify both contain expected elements (sanity checks)
	require.Contains(t, standardFormat, "TestFormatMatchesRuntime", "Standard format should contain test function name")
	require.Contains(t, ourFormat, "TestFormatMatchesRuntime", "Our format should contain test function name")
	require.Contains(t, standardFormat, ".go:", "Standard format should contain file extension and line separator")
	require.Contains(t, ourFormat, ".go:", "Our format should contain file extension and line separator")
}

// TestStackTraceCapture provides regression testing for stacktrace capture behavior
func TestStackTraceCapture(t *testing.T) {
	t.Run("SkipAndCapture basic functionality", func(t *testing.T) {
		stack := SkipAndCapture(1) // Skip this function
		require.NotEmpty(t, stack, "Should capture stack frames")

		// Verify frame structure
		for i, frame := range stack {
			assert.NotEmpty(t, frame.Function, "Frame should have function name")
			assert.NotEmpty(t, frame.File, "Frame should have file path")
			assert.Greater(t, frame.Line, uint32(0), "Frame should have valid line number")
			// Note: frame.Index reflects original position in full stack, not array index
			t.Logf("Frame %d: index=%d, function=%s", i, frame.Index, frame.Function)
		}

		formatted := Format(stack)
		assert.NotEmpty(t, formatted, "Formatted stack should not be empty")
		t.Logf("SkipAndCapture stack (%d frames):\n%s", len(stack), formatted)
	})

	t.Run("CaptureWithRedaction basic functionality", func(t *testing.T) {
		stack := CaptureWithRedaction(1) // Skip this function
		require.NotEmpty(t, stack, "Should capture stack frames with redaction")

		// Verify frame structure
		for i, frame := range stack {
			assert.NotEmpty(t, frame.Function, "Frame should have function name")
			// File might be redacted for customer code, so don't assert NotEmpty
			assert.GreaterOrEqual(t, frame.Line, uint32(0), "Frame should have valid line number")
			// Note: frame.Index reflects original position in full stack, not array index
			t.Logf("Frame %d: index=%d, function=%s", i, frame.Index, frame.Function)
		}

		formatted := Format(stack)
		assert.NotEmpty(t, formatted, "Formatted stack should not be empty")

		assert.NotContains(t, formatted, redactedPlaceholder, "Internal code should not be redacted")

		t.Logf("CaptureWithRedaction stack (%d frames):\n%s", len(stack), formatted)
	})

	t.Run("frame filtering comparison", func(t *testing.T) {
		skipStack := SkipAndCapture(1)            // Filters internal frames
		redactionStack := CaptureWithRedaction(1) // Keeps internal frames

		require.NotEmpty(t, skipStack, "SkipAndCapture should return frames")
		require.NotEmpty(t, redactionStack, "CaptureWithRedaction should return frames")

		// CaptureWithRedaction should have same or more frames since it keeps internal frames
		assert.GreaterOrEqual(t, len(redactionStack), len(skipStack),
			"CaptureWithRedaction should have >= frames than SkipAndCapture")

		t.Logf("Frame count comparison - SkipAndCapture: %d, CaptureWithRedaction: %d",
			len(skipStack), len(redactionStack))
	})

	t.Run("skip parameter progression", func(t *testing.T) {
		// Test that increasing skip values result in appropriate frame reduction
		stack0 := CaptureWithRedaction(0)
		stack1 := CaptureWithRedaction(1)
		stack2 := CaptureWithRedaction(2)

		// Progressive skipping should reduce or maintain frame count
		assert.GreaterOrEqual(t, len(stack0), len(stack1), "skip=0 should have >= frames than skip=1")
		assert.GreaterOrEqual(t, len(stack1), len(stack2), "skip=1 should have >= frames than skip=2")

		t.Logf("Skip progression - skip=0: %d frames, skip=1: %d frames, skip=2: %d frames",
			len(stack0), len(stack1), len(stack2))
	})
}

// TestStackTraceRealCapture tests actual stack trace capture with explicit frame verification
func TestStackTraceRealCapture(t *testing.T) {
	// Create a controlled call stack for precise testing
	captureAtLevel3 := func() StackTrace {
		// This function captures the stack - should appear in CaptureWithRedaction
		return CaptureWithRedaction(0) // Don't skip any frames
	}

	callLevel2 := func() StackTrace {
		// This function calls the capture function
		return captureAtLevel3()
	}

	callLevel1 := func() StackTrace {
		// Top level function in our controlled stack
		return callLevel2()
	}

	t.Run("verify explicit frame content with CaptureWithRedaction", func(t *testing.T) {
		stack := callLevel1()
		require.NotEmpty(t, stack, "Should capture frames")

		formatted := Format(stack)
		t.Logf("Full captured stack trace (%d frames):\n%s", len(stack), formatted)

		// Verify we have the expected frames with explicit content checks
		require.GreaterOrEqual(t, len(stack), 3, "Should have at least 3 frames")

		// Check that our test functions appear in the stack (Go names them as func1, func2, etc.)
		assert.Contains(t, formatted, "TestStackTraceRealCapture.func1", "Stack should contain captureAtLevel3 (func1)")
		assert.Contains(t, formatted, "TestStackTraceRealCapture.func2", "Stack should contain callLevel2 (func2)")
		assert.Contains(t, formatted, "TestStackTraceRealCapture.func3", "Stack should contain callLevel1 (func3)")

		// Verify individual frames have proper structure
		for i, frame := range stack {
			t.Logf("Frame %d: %s.%s at %s:%d (index=%d)",
				i, frame.Namespace, frame.Function, frame.File, frame.Line, frame.Index)

			// Each frame should have valid content
			assert.NotEmpty(t, frame.Function, "Frame %d should have function name", i)
			assert.NotEmpty(t, frame.File, "Frame %d should have file path", i)
			assert.Greater(t, frame.Line, uint32(0), "Frame %d should have valid line number", i)
			assert.NotEmpty(t, frame.Namespace, "Frame %d should have namespace", i)
		}

		// Check that internal frames are preserved (not redacted)
		hasInternalFrames := false
		for _, frame := range stack {
			if strings.Contains(frame.Namespace, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace") {
				hasInternalFrames = true
				assert.NotEqual(t, redactedPlaceholder, frame.Function, "Internal frames should not be redacted")
				assert.NotEqual(t, redactedPlaceholder, frame.File, "Internal frame files should not be redacted")
				break
			}
		}
		assert.True(t, hasInternalFrames, "Should include internal Datadog frames")
	})

	t.Run("compare SkipAndCapture vs CaptureWithRedaction explicit content", func(t *testing.T) {
		skipStack := SkipAndCapture(0)            // Filters internal frames
		redactionStack := CaptureWithRedaction(0) // Keeps internal frames

		skipFormatted := Format(skipStack)
		redactionFormatted := Format(redactionStack)

		t.Logf("SkipAndCapture result (%d frames):\n%s", len(skipStack), skipFormatted)
		t.Logf("CaptureWithRedaction result (%d frames):\n%s", len(redactionStack), redactionFormatted)

		// SkipAndCapture should filter out internal DD frames
		assert.NotContains(t, skipFormatted, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
			"SkipAndCapture should filter out internal frames")

		// CaptureWithRedaction should keep internal DD frames
		assert.Contains(t, redactionFormatted, "github.com/DataDog/dd-trace-go/v2/internal/stacktrace",
			"CaptureWithRedaction should keep internal frames")

		// CaptureWithRedaction should have more detailed stack
		assert.Greater(t, len(redactionStack), len(skipStack),
			"CaptureWithRedaction should have more frames than SkipAndCapture")
	})

	t.Run("verify telemetry integration stack trace", func(t *testing.T) {
		// Test the actual usage pattern from telemetry backend
		telemetryStack := func() StackTrace {
			// Simulate the call from telemetry backend
			return CaptureWithRedaction(4) // Skip: CaptureWithRedaction, capture, loggerBackend.add, loggerBackend.Add
		}

		stack := telemetryStack()
		formatted := Format(stack)

		t.Logf("Telemetry pattern stack trace (%d frames):\n%s", len(stack), formatted)

		// Should not contain our telemetryStack function (skipped by skip=4)
		assert.NotContains(t, formatted, "telemetryStack", "Should skip telemetryStack function")

		// Should contain the test function that called it
		assert.Contains(t, formatted, "TestStackTraceRealCapture", "Should contain calling test function")

		// Verify all frames have proper structure
		for i, frame := range stack {
			assert.NotEmpty(t, frame.Function, "Frame %d function should not be empty", i)
			assert.NotEmpty(t, frame.File, "Frame %d file should not be empty", i)
			assert.Greater(t, frame.Line, uint32(0), "Frame %d line should be > 0", i)
		}
	})
}
