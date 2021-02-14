// +build runtime_instrumentation

package erpc

import "runtime"

// Goid returns the goid of the current goroutine
func Goid() int64 {
	return runtime.Goid()
}

// Mid returns the machine thread id on which the current goroutine runs
func Mid() uint64 {
	return runtime.Mid()
}
