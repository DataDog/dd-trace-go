// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package errors

import (
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

type withStack struct {
	err   error
	stack []uintptr
}

func (e *withStack) Error() string       { return e.err.Error() }
func (e *withStack) Unwrap() error       { return e.err }
func (e *withStack) StackPCs() []uintptr { return append([]uintptr(nil), e.stack...) }

func WithStack(err error) error {
	if err == nil {
		return nil
	}
	var ws *withStack
	if errors.As(err, &ws) { // already has a stack
		return err
	}
	const depth = 32
	pcs := make([]uintptr, depth)
	n := runtime.Callers(2, pcs) // skip Callers + WithStack
	return &withStack{err: err, stack: pcs[:n]}
}

// ErrorType returns the wrapped error type in case it was created from this package, otherwise it returns the given error type.
func ErrorType(err error) string {
	var originalErr error
	var ws *withStack
	if !errors.As(err, &ws) {
		originalErr = err
	} else {
		originalErr = ws.Unwrap()
	}
	return reflect.TypeOf(originalErr).String()
}

func StackTrace(err error) string {
	var ws *withStack
	if !errors.As(err, &ws) {
		return ""
	}

	var builder strings.Builder
	frames := runtime.CallersFrames(ws.StackPCs())
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
	return builder.String()
}
