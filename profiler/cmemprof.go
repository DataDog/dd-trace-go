//go:build cgo && experimental_cmemprof && (linux || darwin)
// +build cgo
// +build experimental_cmemprof
// +build linux darwin

package profiler

import _ "gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/cmemprof"
