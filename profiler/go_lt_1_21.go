//go:build !go1.21

package profiler

func init() {
	executionTraceEnabledDefault = false
}
