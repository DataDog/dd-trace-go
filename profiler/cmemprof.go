//go:build cgo && experimental_cmemprof && (linux || darwin)

package profiler

import _ "gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/cmemprof"
