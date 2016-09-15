package tracer

import (
	"time"
)

// Now returns a timestamp in our nanoseconds default format.
// Changing this method has side-effects in the whole
// package
func Now() int64 {
	return time.Now().UnixNano()
}
