// +build !runtime_instrumentation

package erpc

import (
	"github.com/nikandfor/goid"
	"syscall"
)

// Goid returns the goid of the current goroutine
func Goid() int64 {
	return goid.ID()
}

// Mid returns the machine thread id on which the current goroutine runs
func Mid() uint64 {
	return uint64(syscall.Gettid())
}
