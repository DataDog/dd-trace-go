// +build !windows,!linux,!darwin

package tracer

import (
	"runtime"
)

func osName() string {
	return runtime.GOOS
}

func osVersion() string {
	return "(Unknown Version)"
}
