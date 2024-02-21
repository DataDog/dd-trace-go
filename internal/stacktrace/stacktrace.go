// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate msgp -o=stacktrace_msgp.go -tests=false

package stacktrace

import (
	"runtime"
	"strings"
)

const defaultMaxDepth = 32
const defaultCallerSkip = 4

// StackTrace is intended to be sent over the span tag `_dd.stack`, the first frame is the top of the stack
type StackTrace []StackFrame

// Represents a single frame in the stack trace
type StackFrame struct {
	Index     uint32 `msg:"id"`                   // Index of the frame (0 = top of the stack)
	Text      string `msg:"text,omitempty"`       // Text version of the stackframe as a string
	File      string `msg:"file,omitempty"`       // File name where the code line is
	Line      uint32 `msg:"line,omitempty"`       // Line number in the context of the file where the code is
	Column    uint32 `msg:"column,omitempty"`     // Column where the code ran is
	Namespace string `msg:"namespace,omitempty"`  // Namespace is the fully qualified name of the package where the code is
	ClassName string `msg:"class_name,omitempty"` // ClassName is the fully qualified name of the class where the line of code is
	Function  string `msg:"function,omitempty"`   // Function is the fully qualified name of the function where the line of code is
}

// getPackageFromFunctionName returns the package name from a function name
// ex: gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Event).NewException -> gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace
func getPackageFromFunctionName(name string) string {
	index := strings.LastIndex(name, ".")
	if index == -1 || index == 0 {
		return name
	}

	// If the last character before the last dot is a closing parenthesis, it means that the function is a method
	// so we need to find the last dot before the receiver
	if name[index-1] == ')' {
		index = strings.LastIndex(name[:index], ".")
		if index == -1 {
			return name[:index]
		}
	}

	return name[:index]
}

// getMethodReceiverFromFunctionName returns the receiver of a method from a function name
// ex: gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Event).NewException -> *Event
func getMethodReceiverFromFunctionName(name string) string {
	startIndex := strings.Index(name, "(")
	if startIndex == -1 {
		// There is no classname (method receiver) in the function name
		return ""
	}

	index := strings.LastIndex(name, ".")
	if index == -1 {
		return name
	}

	return name[:index]
}

// Take Create a new stack trace from the current call stack
func Take() StackTrace {
	return TakeWithSkip(4)
}

// TakeWithSkip creates a new stack trace from the current call stack, skipping the first `skip` frames
func TakeWithSkip(skip int) StackTrace {
	frames, depth := callers(skip, defaultMaxDepth)
	stack := make([]StackFrame, depth)

	// There can be way more frames than callers, so we need to check again that we don't store more frames that the depth specified
	var frame runtime.Frame
	for i := depth - 1; i >= 0; i-- {
		frame, _ = frames.Next()
		stack[i] = StackFrame{
			Index:     uint32(i),
			Text:      "",
			File:      frame.File,
			Line:      uint32(frame.Line),
			Column:    0, // No column given by the runtime
			Namespace: getPackageFromFunctionName(frame.Function),
			ClassName: getMethodReceiverFromFunctionName(frame.Function),
			Function:  frame.Function,
		}
	}

	return stack
}

func callers(skip, maxDepth int) (*runtime.Frames, int) {
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(skip, pcs[:])

	depth := maxDepth
	// Find the real depth of the stack (if the stack is smaller than the max depth)
	for ; depth > 0; depth-- {
		if pcs[depth-1] != 0 {
			break
		}
	}

	return runtime.CallersFrames(pcs[:n]), depth
}
