package ext

import (
	"runtime"
	"strings"
)

const (
	Lang          = "go"
	Interpreter   = runtime.Compiler + "_" + runtime.GOOS + "_" + runtime.GOARCH
	TracerVersion = "v0.5.0"
)

var LangVersion = strings.TrimPrefix(runtime.Version(), Lang)
