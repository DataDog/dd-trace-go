// +build !go1.11

package tracer

func startExecutionTracerTask(name string) func() {
	return func() {}
}
