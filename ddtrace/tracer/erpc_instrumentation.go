package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	_ "unsafe"
)

//go:linkname _ddog_runtime_execute runtime._ddog_runtime_execute
func _ddog_runtime_execute(goID int64, tid uint64) {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		if t.eRPCClient != nil {
			t.eRPCClient.HandleRuntimeExecuteEvent(goID, tid)
		}
	}
	return
}
