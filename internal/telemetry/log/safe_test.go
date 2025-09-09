// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"errors"
	"log/slog"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSafeError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType string
	}{
		{
			name:     "nil error",
			err:      nil,
			wantType: nilErrorType,
		},
		{
			name:     "standard error",
			err:      errors.New("sensitive data"),
			wantType: "errors.errorString",
		},
		{
			name:     "custom error type",
			err:      &customError{msg: "secret info"},
			wantType: "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.customError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safeErr := NewSafeError(tt.err)

			require.Equal(t, tt.wantType, safeErr.errType)

			if tt.err != nil {
				require.NotEmpty(t, safeErr.redactedStack, "Expected stack trace for non-nil error")
			}
		})
	}
}

func TestSafeError_LogValue_NoSensitiveData(t *testing.T) {
	sensitiveErr := errors.New("password: secret123")
	safeErr := NewSafeError(sensitiveErr)

	logVal := safeErr.LogValue()
	logStr := logVal.String()

	// Verify no sensitive data leaks
	require.NotContains(t, logStr, "password", "SafeError.LogValue() leaked password")
	require.NotContains(t, logStr, "secret123", "SafeError.LogValue() leaked secret")

	// Verify only error type is present
	require.Contains(t, logStr, "errors.errorString", "SafeError.LogValue() missing error type")
}

func TestStackRedaction_CustomerFrames(t *testing.T) {
	safeErr := NewSafeError(errors.New("test"))

	customerFrameFound := false
	for _, frame := range safeErr.redactedStack {
		if frame.frameType == frameTypeCustomer {
			customerFrameFound = true

			require.Equal(t, redactedPlaceholder, frame.function, "Customer frame function not redacted")
			require.Equal(t, redactedPlaceholder, frame.file, "Customer frame file not redacted")
			require.Equal(t, 0, frame.line, "Customer frame line not redacted")
		}
	}

	if !customerFrameFound {
		t.Skip("No customer frames found in stack - test may be incomplete in test environment")
	}
}

func TestFrameClassification(t *testing.T) {
	tests := []struct {
		function string
		want     frameType
	}{
		{
			function: "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.StartSpan",
			want:     frameTypeDatadog,
		},
		{
			function: "runtime.main",
			want:     frameTypeRuntime,
		},
		{
			function: "net/http.(*Server).Serve",
			want:     frameTypeRuntime,
		},
		{
			function: "github.com/gin-gonic/gin.(*Engine).ServeHTTP",
			want:     frameTypeThirdParty,
		},
		{
			function: "github.com/gorilla/mux.(*Router).ServeHTTP",
			want:     frameTypeThirdParty,
		},
		{
			function: "main.someCustomerFunction",
			want:     frameTypeCustomer,
		},
		{
			function: "github.com/customer/app/pkg.SomeFunction",
			want:     frameTypeCustomer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.function, func(t *testing.T) {
			frame := runtime.Frame{Function: tt.function}
			got := classifyFrame(frame)

			require.Equal(t, tt.want, got, "classifyFrame(%s) = %v, want %v", tt.function, got, tt.want)
		})
	}
}

func TestIsStandardLibrary(t *testing.T) {
	tests := []struct {
		name     string
		function string
		want     bool
	}{
		// Standard library - single element packages
		{"fmt", "fmt.Println", true},
		{"runtime", "runtime.main", true},

		// Standard library - multi-element packages
		{"net/http method", "net/http.(*Server).Serve", true},
		{"net/http function", "net/http.Get", true},
		{"encoding/json", "encoding/json.Marshal", true},
		{"crypto/tls", "crypto/tls.(*Conn).Handshake", true},
		{"unicode/utf8", "unicode/utf8.RuneCountInString", true},

		// Standard library - special cases
		{"go tools", "go/ast.(*File).End", true},
		{"cmd tools", "cmd/link/internal/ld.main", true},
		{"internal packages", "internal/reflectlite.(*rtype).Method", true},

		// Non-standard library
		{"main package", "main.main", false},
		{"main custom function", "main.customFunction", false},
		{"third-party github", "github.com/user/repo.Func", false},
		{"third-party github complex", "github.com/external/pkg.Function", false},
		{"third-party gopkg.in", "gopkg.in/yaml.v3.Unmarshal", false},
		{"datadog repo", "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log.isStandardLibrary", false},

		// Edge cases
		{"malformed no dot", "net/http/Server", false}, // defensively false when no '.' in input
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStandardLibrary(tt.function)
			require.Equal(t, tt.want, got, "isStandardLibrary(%q) = %v, want %v", tt.function, got, tt.want)
		})
	}
}

func TestSafeError_LogValue_Structure(t *testing.T) {
	safeErr := NewSafeError(errors.New("test"))
	logVal := safeErr.LogValue()

	require.Equal(t, slog.KindGroup, logVal.Kind(), "SafeError.LogValue() kind = %v, want %v", logVal.Kind(), slog.KindGroup)

	// Verify required fields are present
	logStr := logVal.String()
	require.Contains(t, logStr, "error_type", "Missing error_type field in log output")
	require.Contains(t, logStr, "stack", "Missing stack field in log output")
}

type customError struct {
	msg string
}

func (e *customError) Error() string {
	return e.msg
}
