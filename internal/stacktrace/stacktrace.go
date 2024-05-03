// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate msgp -o=stacktrace_msgp.go -tests=false

package stacktrace

import (
	"errors"
	"os"
	"regexp"
	"runtime"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/hashicorp/go-secure-stdlib/parseutil"
)

var (
	enabled              = true
	defaultTopFrameDepth = 8
	defaultMaxDepth      = 32
)

const (
	defaultCallerSkip    = 4
	envStackTraceDepth   = "DD_APPSEC_MAX_STACK_TRACE_DEPTH"
	envStackTraceEnabled = "DD_APPSEC_STACK_TRACE_ENABLE"
)

func init() {
	if env := os.Getenv(envStackTraceEnabled); env != "" {
		if e, err := parseutil.ParseBool(env); err == nil {
			enabled = e
		} else {
			log.Error("Failed to parse %s env var as boolean: %v (using default value: %v)", envStackTraceEnabled, err, enabled)
		}
	}

	if env := os.Getenv(envStackTraceDepth); env != "" {
		if !enabled {
			log.Warn("Ignoring %s because stacktrace generation is disable", envStackTraceDepth)
			return
		}

		if depth, err := parseutil.SafeParseInt(env); err == nil {
			defaultMaxDepth = depth
		} else {
			if depth <= 0 && err == nil {
				err = errors.New("value is not a strictly positive integer")
			}
			log.Error("Failed to parse %s env var as a positive integer: %v (using default value: %v)", envStackTraceDepth, err, defaultMaxDepth)
		}
	}

	defaultTopFrameDepth = defaultMaxDepth / 4
}

// Enabled returns whether stacktrace should be collected
func Enabled() bool {
	return enabled
}

// StackTrace is intended to be sent over the span tag `_dd.stack`, the first frame is the current frame
type StackTrace []StackFrame

// StackFrame represents a single frame in the stack trace
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

type symbol struct {
	Package  string
	Receiver string
	Function string
}

var symbolRegex = regexp.MustCompile(`^(([^(]+/)?([^(/.]+)?)(\.\(([^/)]+)\))?\.([^/()]+)$`)

// parseSymbol parses a symbol name into its package, receiver and function
// ex: gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.(*Event).NewException
// -> package: gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace
// -> receiver: *Event
// -> function: NewException
func parseSymbol(name string) symbol {
	matches := symbolRegex.FindStringSubmatch(name)
	if len(matches) != 7 {
		log.Error("Failed to parse symbol for stacktrace: %s", name)
		return symbol{
			Package:  "",
			Receiver: "",
			Function: "",
		}
	}

	return symbol{
		Package:  matches[1],
		Receiver: matches[5],
		Function: matches[6],
	}
}

// Capture create a new stack trace from the current call stack
func Capture() StackTrace {
	return SkipAndCapture(defaultCallerSkip)
}

// SkipAndCapture creates a new stack trace from the current call stack, skipping the first `skip` frames
func SkipAndCapture(skip int) StackTrace {
	// callers() and getRealStackDepth() have to be used side by side to keep the same number of `skip`-ed frames
	realDepth := getRealStackDepth(skip, defaultMaxDepth)
	callers := callers(skip, realDepth, defaultMaxDepth, defaultTopFrameDepth)
	frames := callersFrame(callers, defaultMaxDepth, defaultTopFrameDepth)
	stack := make([]StackFrame, len(frames))

	for i := 0; i < len(frames); i++ {
		frame := frames[i]

		// If the top frames are separated from the bottom frames we have to stitch the real index together
		frameIndex := i
		if frameIndex >= defaultMaxDepth-defaultTopFrameDepth {
			frameIndex = realDepth - defaultMaxDepth + i
		}

		parsedSymbol := parseSymbol(frame.Function)

		stack[i] = StackFrame{
			Index:     uint32(frameIndex),
			Text:      "",
			File:      frame.File,
			Line:      uint32(frame.Line),
			Column:    0, // No column given by the runtime
			Namespace: parsedSymbol.Package,
			ClassName: parsedSymbol.Receiver,
			Function:  parsedSymbol.Function,
		}
	}

	return stack
}

// getRealStackDepth returns the real depth of the stack, skipping the first `skip` frames
func getRealStackDepth(skip, increment int) int {
	pcs := make([]uintptr, increment)

	depth := increment
	for n := increment; n == increment; depth += n {
		n = runtime.Callers(depth+skip, pcs[:])
	}

	return depth
}

// callers returns an array of function pointers of size stackSize, skipping the first `skip` frames
// if realDepth of the current call stack is bigger that stackSize, we return the first stackSize - defaultTopFrameDepth frames
// and the last defaultTopFrameDepth frames of the whole stack
func callers(skip, realDepth, stackSize, topFrames int) []uintptr {
	// The stack size is smaller than the max depth, return the whole stack
	if realDepth <= stackSize {
		pcs := make([]uintptr, realDepth)
		runtime.Callers(skip, pcs[:])
		return pcs
	}

	// The stack is bigger than the max depth, proceed to find the N start frames and stitch them to the ones we have
	pcs := make([]uintptr, stackSize)
	runtime.Callers(skip, pcs[:stackSize-topFrames])
	runtime.Callers(skip+realDepth-topFrames, pcs[stackSize-topFrames:])
	return pcs
}

// callersFrame returns an array of runtime.Frame from an array of function pointers
// There can be multiple frames for a single function pointer, so we have to cut things again to make sure the final
// stacktrace is the correct size
func callersFrame(pcs []uintptr, stackSize, topFrames int) []runtime.Frame {
	frames := runtime.CallersFrames(pcs)
	framesArray := make([]runtime.Frame, 0, len(pcs))

	for {
		frame, more := frames.Next()
		framesArray = append(framesArray, frame)
		if !more {
			break
		}
	}

	if len(framesArray) > stackSize {
		framesArray = append(framesArray[:stackSize-topFrames], framesArray[len(framesArray)-topFrames:]...)
	}

	return framesArray
}
