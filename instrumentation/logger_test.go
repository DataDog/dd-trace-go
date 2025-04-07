// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"errors"
	"fmt"
	"testing"
)

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want bool
	}{
		{"No arguments", []any{}, false},
		{"Only non-error arguments", []any{42, "hello", true}, false},
		{"Single error argument", []any{errors.New("test error")}, true},
		{"Multiple arguments with one error", []any{"data", 123, errors.New("some error")}, true},
		{"Multiple error arguments", []any{errors.New("error 1"), errors.New("error 2")}, true},
		{"Pointer to an error", []any{&struct{ error }{errors.New("pointer error")}}, true},
		{"Wrapped error with fmt.Errorf", []any{fmt.Errorf("wrapped: %w", errors.New("wrapped error"))}, true},
		{"Error inside a slice (should return false)", []any{[]any{errors.New("hidden error")}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasErrors(tt.args...)
			if got != tt.want {
				t.Errorf("hasErrors(%v) = %v; want %v", tt.args, got, tt.want)
			}
		})
	}
}
