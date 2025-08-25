// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
const (
	msgpackArrayFix byte = 144  // up to 15 items
	msgpackArray16  byte = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
	msgpackArray32  byte = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
)

// traceChunk represents a list of spans with the same trace ID,
// i.e. a chunk of a trace
type traceChunk struct {
	// the sampling priority of the trace
	priority int32

	// the optional string origin ("lambda", "rum", etc.) of the trace chunk
	origin uint32

	// a collection of key to value pairs common in all `spans`
	attributes map[uint32]AnyValue

	// a list of spans in this chunk
	spans []Span

	// whether the trace only contains analyzed spans
	// (not required by tracers and set by the agent)
	droppedTrace bool

	// the ID of the trace to which all spans in this chunk belong
	traceID uint8

	// the optional string decision maker (previously span tag _dd.p.dm)
	decisionMaker uint32
}
