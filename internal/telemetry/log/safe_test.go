// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"errors"
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

type customError struct {
	msg string
}

func (e *customError) Error() string {
	return e.msg
}
