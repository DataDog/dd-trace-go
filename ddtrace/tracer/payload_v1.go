// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync/atomic"

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

type stringTable struct {
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

const (
	StringValueType  = iota + 1 // string or uint -- 1
	BoolValueType               // boolean -- 2
	FloatValueType              // float64 -- 3
	IntValueType                // uint64 -- 4
	BytesValueType              // []uint8 -- 5
	ArrayValueType              // []AnyValue -- 6
	keyValueListType            // []keyValue -- 7
)

type arrayValue []anyValue

// keys in a keyValue can either be a string or a uint32 index
// isString is true when the key is a string value, and false when the key is a uint32 index
type streamingKey struct {
	isString    bool
	stringValue string
	idx         uint32
}

// keyValue is made up of the key and an AnyValue (the type of the value and the value itself)
// The key is either a uint32 index into the string table or a string value.
type keyValue struct {
	key   streamingKey
	value anyValue
}

type keyValueList []keyValue

type spanListV1 spanList

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
	origin, priority := "", 0
	for _, span := range t {
		if span == nil {
			continue
		}
		if p, ok := span.Context().SamplingPriority(); ok {
			origin = span.Context().origin
			priority = p
			break
		}
	}

	p.chunks = append(p.chunks, traceChunk{
		priority:   int32(priority),
		origin:     origin,
		attributes: make(map[uint32]anyValue),
		spans:      t,
		traceID:    t[0].Context().traceID,
	})
	wr := msgp.NewWriter(&p.buf)
	err = t.EncodeMsg(wr, p)
	if err == nil {
		err = wr.Flush()
	}
	if err != nil {
		return payloadStats{}, err
	}
	p.recordItem()
	return p.stats(), nil
}

func (p *payloadV1) grow(n int) {
	p.buf.Grow(n)
}

func (p *payloadV1) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

func (p *payloadV1) clear() {
	p.buf = bytes.Buffer{}
	p.reader = nil
}

func (p *payloadV1) recordItem() {
	atomic.AddUint32(&p.count, 1)
	p.updateHeader()
}

func (p *payloadV1) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: p.itemCount(),
	}
}

func (p *payloadV1) size() int {
	return p.buf.Len() + len(p.header) - p.off
}

func (p *payloadV1) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

func (p *payloadV1) protocol() float64 {
	return p.protocolVersion
}

func (p *payloadV1) updateHeader() {
	n := uint64(atomic.LoadUint32(&p.count))
	switch {
	case n <= 15:
		p.header[7] = msgpackArrayFix + byte(n)
		p.off = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(p.header, n) // writes 2 bytes
		p.header[5] = msgpackArray16
		p.off = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(p.header, n) // writes 4 bytes
		p.header[3] = msgpackArray32
		p.off = 3
	}
}

// Close implements io.Closer
func (p *payloadV1) Close() error {
	return nil
}

// Write implements io.Writer. It writes data directly to the buffer.
func (p *payloadV1) Write(data []byte) (n int, err error) {
	return p.buf.Write(data)
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payloadV1) Read(b []byte) (n int, err error) {
	panic("not implemented")
}

// Encode the anyValue
func (a *anyValue) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	switch a.valueType {
	case StringValueType:
		e.WriteInt32(StringValueType)
		v, err := p.encodeString(a.value.(string))
		if err != nil {
			return err
		}
		if v.isString {
			return e.WriteString(v.stringValue)
		}
		return e.WriteUint32(v.idx)
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
	case ArrayValueType:
		e.WriteInt32(ArrayValueType)
		return a.value.(arrayValue).EncodeMsg(e, p)
	case keyValueListType:
		e.WriteInt32(keyValueListType)
		return a.value.(keyValueList).EncodeMsg(e, p)
	default:
		return fmt.Errorf("invalid value type: %d", a.valueType)
	}
}

func (av arrayValue) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	err := e.WriteArrayHeader(uint32(len(av)))
	if err != nil {
		return err
	}
	for _, value := range av {
		if err := value.EncodeMsg(e, p); err != nil {
			return err
		}
	}
	return nil
}

func (k keyValue) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	var err error
	if k.key.isString {
		err = e.WriteString(k.key.stringValue)
	} else {
		err = e.WriteUint32(k.key.idx)
	}
	if err != nil {
		return err
	}
	err = k.value.EncodeMsg(e, p)
	if err != nil {
		return err
	}
	return nil
}

func (kv keyValueList) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	err := e.WriteMapHeader(uint32(len(kv)))
	if err != nil {
		return err
	}
	for i, k := range kv {
		err := e.WriteUint32(uint32(i))
		if err != nil {
			return err
		}
		err = k.EncodeMsg(e, p)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *traceChunk) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	kv := keyValueList{
		{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: t.priority}},          // priority
		{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: t.origin}},         // origin
		{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: keyValueListType, value: t.spans}},         // spans
		{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: BoolValueType, value: t.droppedTrace}},     // droppedTrace
		{key: streamingKey{isString: false, idx: 6}, value: anyValue{valueType: BytesValueType, value: t.traceID}},         // traceID
		{key: streamingKey{isString: false, idx: 7}, value: anyValue{valueType: IntValueType, value: t.samplingMechanism}}, // samplingMechanism
	}

	attr := keyValueList{}
	for k, v := range t.attributes {
		attr = append(attr, keyValue{key: streamingKey{isString: false, idx: k}, value: anyValue{valueType: getAnyValueType(v), value: v}})
	}
	kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: ArrayValueType, value: attr}}) // attributes

	return kv.EncodeMsg(e, p)
}

// EncodeMsg writes the contents of a list of spans into `p.buf`
// Span, SpanLink, and SpanEvent structs are different for v0.4 and v1.0.
// For v1 we need to manually encode the spans, span links, and span events
// if we don't want to do extra allocations.
func (s spanListV1) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
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
			err := encodeSpan(span, e, p)
			if err != nil {
				return msgp.WrapError(err, span)
			}
		}
	}

	return nil
}

// Custom encoding for spans under the v1 trace protocol.
// The encoding of attributes is the combination of the meta, metrics, and metaStruct fields of the v0.4 protocol.
func encodeSpan(s *Span, e *msgp.Writer, p *payloadV1) error {
	kv := keyValueList{
		{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: StringValueType, value: s.service}},      // service
		{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: s.name}},         // name
		{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: StringValueType, value: s.resource}},     // resource
		{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: IntValueType, value: s.spanID}},          // spanID
		{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: IntValueType, value: s.parentID}},        // parentID
		{key: streamingKey{isString: false, idx: 6}, value: anyValue{valueType: IntValueType, value: s.start}},           // start
		{key: streamingKey{isString: false, idx: 7}, value: anyValue{valueType: IntValueType, value: s.duration}},        // duration
		{key: streamingKey{isString: false, idx: 8}, value: anyValue{valueType: BoolValueType, value: s.error}},          // error
		{key: streamingKey{isString: false, idx: 10}, value: anyValue{valueType: StringValueType, value: s.spanType}},    // type
		{key: streamingKey{isString: false, idx: 11}, value: anyValue{valueType: keyValueListType, value: s.spanLinks}},  // SpanLink
		{key: streamingKey{isString: false, idx: 12}, value: anyValue{valueType: keyValueListType, value: s.spanEvents}}, // SpanEvent
		{key: streamingKey{isString: false, idx: 15}, value: anyValue{valueType: StringValueType, value: s.integration}}, // component
	}

	// encode meta attributes
	attr := keyValueList{}
	for k, v := range s.meta {
		idx, err := p.encodeString(k)
		if err != nil {
			idx = streamingKey{isString: true, stringValue: k}
		}
		attr = append(attr, keyValue{key: idx, value: anyValue{valueType: StringValueType, value: v}})
	}

	// encode metric attributes
	for k, v := range s.metrics {
		idx, err := p.encodeString(k)
		if err != nil {
			idx = streamingKey{isString: true, stringValue: k}
		}
		attr = append(attr, keyValue{key: idx, value: anyValue{valueType: FloatValueType, value: v}})
	}

	// encode metaStruct attributes
	for k, v := range s.metaStruct {
		idx, err := p.encodeString(k)
		if err != nil {
			idx = streamingKey{isString: true, stringValue: k}
		}
		attr = append(attr, keyValue{key: idx, value: anyValue{valueType: getAnyValueType(v), value: v}})
	}

	kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 9}, value: anyValue{valueType: ArrayValueType, value: attr}}) // attributes

	env, ok := s.meta["env"]
	if ok {
		kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 13}, value: anyValue{valueType: StringValueType, value: env}}) // env
	}
	version, ok := s.meta["version"]
	if ok {
		kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 14}, value: anyValue{valueType: StringValueType, value: version}}) // version
	}

	return kv.EncodeMsg(e, p)
}

// encodeString and decodeString handles encoding a string to the payload's string table.
// When writing a string:
// - use its index in the string table if it exists
// - otherwise, write the string into the message, then add the string at the next index
// Returns the index of the string in the string table, and an error if there is one
func (p *payloadV1) encodeString(s string) (streamingKey, error) {
	sTable := &p.strings
	idx, ok := sTable.indices[s]
	// if the string already exists in the table, use its index
	if ok {
		return streamingKey{isString: false, idx: idx}, nil
	}

	// else, write the string into the table at the next index
	// return an error to indicate that the string should be written to the msgp message
	sTable.indices[s] = sTable.nextIndex
	sTable.strings = append(sTable.strings, s)
	sTable.nextIndex += 1
	return streamingKey{isString: true, stringValue: s}, nil
}

// encodeSpanLinks encodes the span links into a msgp.Writer
// Span links are represented as an array of fixmaps (keyValueList)
func encodeSpanLinks(sl []SpanLink, e *msgp.Writer, p *payloadV1) error {
	// write the number of span links
	err := e.WriteArrayHeader(uint32(len(sl)))
	if err != nil {
		return err
	}

	// represent each span link as a fixmap (keyValueList) and add it to an array
	kv := arrayValue{}
	for _, s := range sl {
		slKeyValues := keyValueList{
			{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: s.TraceID}},       // traceID
			{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: IntValueType, value: s.SpanID}},        // spanID
			{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: StringValueType, value: s.Tracestate}}, // tracestate
			{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: IntValueType, value: s.Flags}},         // flags
		}

		attr := keyValueList{}
		// attributes
		for k, v := range s.Attributes {
			idx, err := p.encodeString(k)
			if err != nil {
				idx = streamingKey{isString: true, stringValue: k}
			}
			attr = append(attr, keyValue{key: idx, value: anyValue{valueType: getAnyValueType(v), value: v}})
		}
		slKeyValues = append(slKeyValues, keyValue{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: ArrayValueType, value: attr}}) // attributes
		kv = append(kv, anyValue{valueType: keyValueListType, value: slKeyValues})
	}

	for _, v := range kv {
		err := v.EncodeMsg(e, p)
		if err != nil {
			return err
		}
	}
	return nil
}

// encodeSpanEvents encodes the span events into a msgp.Writer
// Span events are represented as an array of fixmaps (keyValueList)
func encodeSpanEvents(se []spanEvent, e *msgp.Writer, p *payloadV1) error {
	// write the number of span events
	err := e.WriteArrayHeader(uint32(len(se)))
	if err != nil {
		return err
	}

	// represent each span event as a fixmap (keyValueList) and add it to an array
	kv := arrayValue{}
	for _, s := range se {
		slKeyValues := keyValueList{
			{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: s.TimeUnixNano}}, // time
			{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: s.Name}},      // name
		}

		attr := keyValueList{}
		// attributes
		for k, v := range s.Attributes {
			idx, err := p.encodeString(k)
			if err != nil {
				idx = streamingKey{isString: true, stringValue: k}
			}
			attr = append(attr, keyValue{key: idx, value: anyValue{valueType: getAnyValueType(v), value: v}})
		}
		slKeyValues = append(slKeyValues, keyValue{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: ArrayValueType, value: attr}}) // attributes
		kv = append(kv, anyValue{valueType: keyValueListType, value: slKeyValues})
	}

	for _, v := range kv {
		err := v.EncodeMsg(e, p)
		if err != nil {
			return err
		}
	}
	return nil
}

func getAnyValueType(v any) int {
	switch v.(type) {
	case string:
		return StringValueType
	case bool:
		return BoolValueType
	case float64:
		return FloatValueType
	case float32:
		return FloatValueType
	case []byte:
		return BytesValueType
	}
	return IntValueType
}
