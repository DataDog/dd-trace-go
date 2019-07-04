// +build go1.11

package tracer

import (
	"context"
	t "runtime/trace"
)

func startExecutionTracerTask(name string) func() {
	if !t.IsEnabled() {
		return func() {}
	}
	_, task := t.NewTask(context.TODO(), name)
	return task.End
}
