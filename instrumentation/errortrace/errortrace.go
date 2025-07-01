// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package errortrace

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
)

// TracerError is an error type that holds stackframes from when the error was thrown.
// It can be used interchangeably with the built-in Go error type.
type TracerError struct {
	stackFrames *runtime.Frames
	inner       error
	stack       *bytes.Buffer
}

// defaultStackLength specifies the default maximum size of a stack trace.
const defaultStackLength = 32

func (err *TracerError) Error() string {
	return err.inner.Error()
}

func New(text string) *TracerError {
	// Skip one to exclude New(...)
	return Wrap(errors.New(text), 0, 1)
}

// Wrap takes in an error and records the stack trace at the moment that it was thrown.
// TODO: this still doesn't find the root cause of an error.
func Wrap(err error, n uint, skip uint) *TracerError {
	if err == nil {
		return nil
	}
	if e, ok := err.(*TracerError); ok {
		return e // TODO: what happens if users specify n/skip here, but created err using New()...?
	}
	if n <= 0 {
		n = defaultStackLength
	}

	pcs := make([]uintptr, n)
	var stackFrames *runtime.Frames
	// +2 to exclude runtime.Callers and Wrap
	numFrames := runtime.Callers(2+int(skip), pcs)
	if numFrames == 0 {
		stackFrames = nil
	} else {
		stackFrames = runtime.CallersFrames(pcs[:numFrames])
	}

	tracerErr := &TracerError{
		stackFrames: stackFrames,
		inner:       err,
	}
	return tracerErr
}

// Format returns a string representation of the stack trace.
func (err *TracerError) Format() string {
	if err == nil || err.stackFrames == nil {
		return ""
	}
	if err.stack != nil {
		return err.stack.String()
	}

	out := bytes.Buffer{}
	for i := 0; ; i++ {
		frame, more := err.stackFrames.Next()
		if i != 0 {
			out.WriteByte('\n')
		}
		out.WriteString(frame.Function)
		out.WriteByte('\n')
		out.WriteByte('\t')
		out.WriteString(frame.File)
		out.WriteByte(':')
		out.WriteString(strconv.Itoa(frame.Line))
		if !more {
			break
		}
	}
	// CallersFrames returns an iterator that is consumed as we read it. In order to
	// allow calling Format() multiple times, we save the result into err.stack, which can be
	// returned in future calls
	err.stack = &out
	return out.String()
}

// Unwrap takes a wrapped error and returns the inner error.
func (err *TracerError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.inner
}
