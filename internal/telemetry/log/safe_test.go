// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"testing"
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

			if safeErr.errType != tt.wantType {
				t.Errorf("NewSafeError().errType = %v, want %v", safeErr.errType, tt.wantType)
			}

			if tt.err != nil && len(safeErr.redactedStack) == 0 {
				t.Error("Expected stack trace for non-nil error")
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
	if strings.Contains(logStr, "password") || strings.Contains(logStr, "secret123") {
		t.Errorf("SafeError.LogValue() leaked sensitive data: %s", logStr)
	}

	// Verify only error type is present
	if !strings.Contains(logStr, "errors.errorString") {
		t.Errorf("SafeError.LogValue() missing error type: %s", logStr)
	}
}

func TestStackRedaction_CustomerFrames(t *testing.T) {
	safeErr := NewSafeError(errors.New("test"))

	customerFrameFound := false
	for _, frame := range safeErr.redactedStack {
		if frame.frameType == frameTypeCustomer {
			customerFrameFound = true

			if frame.function != redactedPlaceholder {
				t.Errorf("Customer frame function not redacted: %s", frame.function)
			}
			if frame.file != redactedPlaceholder {
				t.Errorf("Customer frame file not redacted: %s", frame.file)
			}
			if frame.line != 0 {
				t.Errorf("Customer frame line not redacted: %d", frame.line)
			}
		}
	}

	if !customerFrameFound {
		t.Error("No customer frames found in stack - test may be incomplete")
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

			if got != tt.want {
				t.Errorf("classifyFrame(%s) = %v, want %v", tt.function, got, tt.want)
			}
		})
	}
}

func TestStandardLibraryDetection(t *testing.T) {
	tests := []struct {
		function string
		want     bool
	}{
		{"runtime.main", true},
		{"net/http.Get", true},
		{"encoding/json.Marshal", true},
		{"fmt.Sprintf", true},
		{"github.com/external/pkg.Function", false},
		{"main.customFunction", false}, // main is customer code, not stdlib
	}

	for _, tt := range tests {
		t.Run(tt.function, func(t *testing.T) {
			got := isStandardLibrary(tt.function)
			if got != tt.want {
				t.Errorf("isStandardLibrary(%s) = %v, want %v", tt.function, got, tt.want)
			}
		})
	}
}

func TestSafeError_LogValue_Structure(t *testing.T) {
	safeErr := NewSafeError(errors.New("test"))
	logVal := safeErr.LogValue()

	if logVal.Kind() != slog.KindGroup {
		t.Errorf("SafeError.LogValue() kind = %v, want %v", logVal.Kind(), slog.KindGroup)
	}

	// Verify required fields are present
	logStr := logVal.String()
	if !strings.Contains(logStr, "error_type") {
		t.Error("Missing error_type field in log output")
	}
	if !strings.Contains(logStr, "stack") {
		t.Error("Missing stack field in log output")
	}
}

type customError struct {
	msg string
}

func (e *customError) Error() string {
	return e.msg
}
