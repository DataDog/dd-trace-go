package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

func StartMockedDataStreams() {
	t := &tracer{dataStreams: &datastreams.Processor{}}
	internal.SetGlobalTracer(t)
	internal.Testing = true
}

func StopMockedDataStreams() {
	internal.SetGlobalTracer(&internal.NoopTracer{})
	internal.Testing = false
}
