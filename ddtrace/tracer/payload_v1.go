// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

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
	containerID uint32

	// the string language name of the tracer
	languageName uint32

	// the string language version of the tracer
	languageVersion uint32

	// the string version of the tracer
	tracerVersion uint32

	// the V4 string UUID representation of a tracer session
	runtimeID uint32

	// the optional `env` string tag that set with the tracer
	env uint32

	// the optional string hostname of where the tracer is running
	hostname uint32

	// the optional string `version` tag for the application set in the tracer
	appVersion uint32

	// a collection of key to value pairs common in all `chunks`
	attributes map[uint32]anyValue

	// a list of trace `chunks`
	chunks []traceChunk

	// fields needed to implement unsafePayload interface
	protocolVersion float64
	itemsCount      uint32
}

// AnyValue is a representation of the `any` value. It can take the following types:
// - uint32
// - bool
// - float64
// - uint64
// - uint8
// intValue(5) - 0x405 (4 indicates this is an int AnyType, then 5 is encoded using positive fixed int format)
// stringValue(“a”) - 0x1a161 (1 indicates this is a string, then “a” is encoded using fixstr 0xa161)
// stringValue(2) - 0x102 (1 indicates this is a string, then a positive fixed int of 2 refers the 2nd index of the string table)
type anyValue struct {
	valueType int
	value     interface{}
}

const (
	StringValueType  = iota + 1 // string or uint
	BoolValueType               // boolean
	FloatValueType              // float64
	IntValueType                // uint64
	BytesValueType              // []uint8
	ArrayValueType              // []AnyValue
	keyValueListType            // []keyValue
)

type arrayValue = []anyValue

// keyValue is made up of the key and an AnyValue (the type of the value and the value itself)
type keyValue struct {
	key   uint32
	value anyValue
}

type keyValueList = []keyValue

// newPayloadV1 returns a ready to use payloadV1.
func newPayloadV1() *payloadV1 {
	return &payloadV1{
		protocolVersion: traceProtocolV1,
		strings:         make([]string, 0),
		attributes:      make(map[uint32]anyValue),
		chunks:          make([]traceChunk, 0),
	}
}

func (p *payloadV1) push(t spanList) (stats payloadStats, err error) {
	panic("not implemented")
}

func (p *payloadV1) grow(n int) {
	panic("not implemented")
}

func (p *payloadV1) reset() {
	panic("not implemented")
}

func (p *payloadV1) clear() {
	panic("not implemented")
}

func (p *payloadV1) recordItem() {
	panic("not implemented")
}

func (p *payloadV1) stats() payloadStats {
	panic("not implemented")
}

func (p *payloadV1) size() int {
	panic("not implemented")
}

func (p *payloadV1) itemCount() int {
	panic("not implemented")
}

func (p *payloadV1) protocol() float64 {
	panic("not implemented")
}

// Close implements io.Closer
func (p *payloadV1) Close() error {
	panic("not implemented")
}

// Write implements io.Writer. It writes data directly to the buffer.
func (p *payloadV1) Write(data []byte) (n int, err error) {
	panic("not implemented")
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payloadV1) Read(b []byte) (n int, err error) {
	panic("not implemented")
}
