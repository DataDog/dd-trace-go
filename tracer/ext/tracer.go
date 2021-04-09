package ext

import (
	"runtime"
	"strings"
)

const (
	Lang          = "go"
	Interpreter   = runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS
	TracerVersion = "v0.6.1"
)

var LangVersion = strings.TrimPrefix(runtime.Version(), Lang)
