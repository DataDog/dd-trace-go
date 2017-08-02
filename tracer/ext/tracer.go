package ext

import "runtime"

const (
	Lang          = "go"
	Interpreter   = runtime.Compiler
	TracerVersion = "v0.5.0"
)

var LangVersion = runtime.Version()
