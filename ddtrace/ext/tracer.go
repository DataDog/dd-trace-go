package ext

import (
	"runtime"
	"strings"
)

const (
	// Lang identifies the language used to run the tracer.
	Lang = "go"

	// Interpreter specifies additional information based on the running architecture and OS.
	Interpreter = runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS

	// TracerVersion specifies the version of the tracer.
	TracerVersion = "v0.7.0"
)

// LangVersion specifies the version of Go used to run the tracer.
var LangVersion = strings.TrimPrefix(runtime.Version(), Lang)
