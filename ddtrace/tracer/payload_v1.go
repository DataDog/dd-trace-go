// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/tinylib/msgp/msgp"
)

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
	// stringTable holds references from a string value to an index.
	// the 0th position in the stringTable should always be the empty string.
	strings stringTable `msgp:"strings"`

	// the string ID of the container where the tracer is running
	containerID uint32 `msgp:"containerID"`

	// the string language name of the tracer
	languageName uint32 `msgp:"languageName"`

	// the string language version of the tracer
	languageVersion uint32 `msgp:"languageVersion"`

	// the string version of the tracer
	tracerVersion uint32 `msgp:"tracerVersion"`

	// the V4 string UUID representation of a tracer session
	runtimeID uint32 `msgp:"runtimeID"`

	// the optional `env` string tag that set with the tracer
	env uint32 `msgp:"env,omitempty"`

	// the optional string hostname of where the tracer is running
	hostname uint32 `msgp:"hostname,omitempty"`

	// the optional string `version` tag for the application set in the tracer
	appVersion uint32 `msgp:"appVersion,omitempty"`

	// a collection of key to value pairs common in all `chunks`
	attributes map[uint32]anyValue `msgp:"attributes,omitempty"`

	// a list of trace `chunks`
	chunks []traceChunk `msgp:"chunks,omitempty"`

	// protocolVersion specifies the trace protocol to use.
	protocolVersion float64

	// buf holds the sequence of msgpack-encoded items.
	buf bytes.Buffer

	// reader is used for reading the contents of buf.
	reader *bytes.Reader
}

type stringTable struct {
	m         sync.Mutex
	strings   []string          // list of strings
	indices   map[string]uint32 // map strings to their indices
	nextIndex uint32            // last index of the stringTable
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

var _ msgp.Encodable = (*anyValue)(nil)

const (
	StringValueType  = iota + 1 // string or uint
	BoolValueType               // boolean
	FloatValueType              // float64
	IntValueType                // uint64
	BytesValueType              // []uint8
	ArrayValueType              // []AnyValue
	keyValueListType            // []keyValue
)

type arrayValue []anyValue

// keyValue is made up of the key and an AnyValue (the type of the value and the value itself)
type keyValue struct {
	key   uint32
	value anyValue
}

type keyValueList []keyValue

var _ msgp.Encodable = (*keyValue)(nil)

// newPayloadV1 returns a ready to use payloadV1.
func newPayloadV1() *payloadV1 {
	return &payloadV1{
		protocolVersion: traceProtocolV1,
		attributes:      make(map[uint32]anyValue),
		chunks:          make([]traceChunk, 0),
		strings: stringTable{
			strings:   []string{""},
			indices:   map[string]uint32{"": 0},
			nextIndex: 1,
		},
	}
}

// push pushes a new item into the stream.
func (p *payloadV1) push(t spanListV1) (stats payloadStats, err error) {
	// We need to hydrate the payload with everything we get from the spans.
	// Conceptually, our `t []*Span` corresponds to one `traceChunk`.
	p.chunks = append(p.chunks, traceChunk{
		spans: t,
	})
	if err := msgp.Encode(&p.buf, t); err != nil { // TODO(hannahkm): this needs to call (spanListV1).EncodeMsg
		return payloadStats{}, err
	}
	p.recordItem()
	return p.stats(), nil
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
	// atomic.AddUint32(&p.count, 1)
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
	return p.protocolVersion
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

type spanListV1 spanList

var _ msgp.Encodable = (*spanListV1)(nil)

// Encode the anyValue
// EncodeMsg implements msgp.Encodable.
func (a *anyValue) EncodeMsg(e *msgp.Writer) error {
	switch a.valueType {
	case StringValueType:
		e.WriteInt32(StringValueType)
		return encodeString(a.value.(string), e)
	case BoolValueType:
		e.WriteInt32(BoolValueType)
		return e.WriteBool(a.value.(bool))
	case FloatValueType:
		e.WriteInt32(FloatValueType)
		return e.WriteFloat64(a.value.(float64))
	case IntValueType:
		e.WriteInt32(IntValueType)
		return e.WriteUint64(a.value.(uint64))
	case BytesValueType:
		e.WriteInt32(BytesValueType)
		return e.WriteBytes(a.value.([]byte))
	default:
		return fmt.Errorf("invalid value type: %d", a.valueType)
	}
}

// EncodeMsg implements msgp.Encodable.
func (av arrayValue) EncodeMsg(e *msgp.Writer) error {
	err := e.WriteArrayHeader(uint32(len(av)))
	if err != nil {
		return err
	}
	for _, value := range av {
		if err := value.EncodeMsg(e); err != nil {
			return err
		}
	}
	return nil
}

// EncodeMsg implements msgp.Encodable.
func (k keyValue) EncodeMsg(e *msgp.Writer) error {
	err := e.WriteUint32(k.key)
	if err != nil {
		return err
	}
	err = k.value.EncodeMsg(e)
	if err != nil {
		return err
	}
	return nil
}

func encodeKeyValueList(kv keyValueList, e *msgp.Writer) error {
	err := e.WriteMapHeader(uint32(len(kv)))
	if err != nil {
		return err
	}
	for _, k := range kv {
		if err := k.value.EncodeMsg(e); err != nil {
			return err
		}
	}
	return nil
}

// EncodeMsg writes the contents of the TraceChunk into `p.buf`
// Span, SpanLink, and SpanEvent structs are different for v0.4 and v1.0.
// For v1 we need to manually encode the spans, span links, and span events
// if we don't want to do extra allocations.
// EncodeMsg implements msgp.Encodable.
func (s spanListV1) EncodeMsg(e *msgp.Writer) error {
	err := e.WriteArrayHeader(uint32(len(s)))
	if err != nil {
		return msgp.WrapError(err)
	}

	e.WriteInt32(4)
	for _, span := range s {
		if span == nil {
			err := e.WriteNil()
			if err != nil {
				return err
			}
		} else {
			err := encodeSpan(span, e)
			if err != nil {
				return msgp.WrapError(err, span)
			}
		}
	}

	return nil
}

// Custom encoding for spans under the v1 trace protocol.
func encodeSpan(s *Span, e *msgp.Writer) error {
	panic("not implemented")
}

// encodeString and decodeString handles encoding a string to the payload's string table.
// When writing a string:
// - use its index in the string table if it exists
// - otherwise, write the string into the message, then add the string at the next index
// When reading a string, check that it is a uint and then:
// - if true, check read up the index position and return that position
// - else, add it to thenext index position and return that position
func encodeString(s string, e *msgp.Writer) error {
	panic("not implemented")
}
