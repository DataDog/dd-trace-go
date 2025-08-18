// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
)

// payloadV04 is a wrapper on top of the msgpack encoder which allows constructing an
// encoded array by pushing its entries sequentially, one at a time. It basically
// allows us to encode as we would with a stream, except that the contents of the stream
// can be read as a slice by the msgpack decoder at any time. It follows the guidelines
// from the msgpack array spec:
// https://github.com/msgpack/msgpack/blob/master/spec.md#array-format-family
//
// payloadV04 implements io.Reader and can be used with the decoder directly. To create
// a new payload use the newPayload method.
//
// payloadV04 is not safe for concurrent use.
//
// payloadV04 is meant to be used only once and eventually dismissed with the
// single exception of retrying failed flush attempts.
//
// ⚠️  Warning!
//
// The payloadV04 should not be reused for multiple sets of traces.  Resetting the
// payloadV04 for re-use requires the transport to wait for the HTTP package to
// Close the request body before attempting to re-use it again! This requires
// additional logic to be in place. See:
//
// • https://github.com/golang/go/blob/go1.16/src/net/http/client.go#L136-L138
// • https://github.com/DataDog/dd-trace-go/pull/475
// • https://github.com/DataDog/dd-trace-go/pull/549
// • https://github.com/DataDog/dd-trace-go/pull/976
type payloadV04 struct {
	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// off specifies the current read position on the header.
	off int

	// count specifies the number of items in the stream.
	count uint32

	// buf holds the sequence of msgpack-encoded items.
	buf bytes.Buffer

	// reader is used for reading the contents of buf.
	reader *bytes.Reader
}

// payloadV1 is a new version of a msgp payload that can be sent to the agent.
// Be aware that payloadV1 follows the same rules and constraints as payloadV04. That is:
//
// payloadV1 is not safe for concurrent use
//
// payloadV1 is meant to be used only once and eventually dismissed with the
// single exception of retrying failed flush attempts.
//
// ⚠️  Warning!
//
// The payloadV1 should not be reused for multiple sets of traces.  Resetting the
// payloadV1 for re-use requires the transport to wait for the HTTP package
// Close the request body before attempting to re-use it again!
type payloadV1 struct {
	// array of strings referenced in this tracer payload, its chunks and spans
	strings []string

	// the string ID of the container where the tracer is running
	containerId uint32

	// the string language name of the tracer
	languageName uint32

	// the string language version of the tracer
	languageVersion uint32

	// the string version of the tracer
	tracerVersion uint32

	// the V4 string UUID representation of a tracer session
	runtimeId uint32

	// the optional `env` string tag that set with the tracer
	env uint32

	// the optional string hostname of where the tracer is running
	hostname uint32

	// the optional string `version` tag for the application set in the tracer
	appVersion uint32

	// a collection of key to value pairs common in all `chunks`
	attributes map[uint32]any // TODO: this should be compatible with AnyValue

	// a list of trace `chunks`
	chunks []traceChunk
}

// traceChunk represents a list of spans with the same trace ID,
// i.e. a chunk of a trace
type traceChunk struct {
	// the sampling priority of the trace
	priority int32

	// the optional string origin ("lambda", "rum", etc.) of the trace chunk
	origin uint32

	// a collection of key to value pairs common in all `spans`
	attributes map[uint32]any // TODO: this should be compatible with AnyValue

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
