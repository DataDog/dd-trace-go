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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
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
	strings *stringTable `msgp:"strings"`

	// the string ID of the container where the tracer is running
	containerID string `msgp:"containerID"`

	// the string language name of the tracer
	languageName string `msgp:"languageName"`

	// the string language version of the tracer
	languageVersion string `msgp:"languageVersion"`

	// the string version of the tracer
	tracerVersion string `msgp:"tracerVersion"`

	// the V4 string UUID representation of a tracer session
	runtimeID string `msgp:"runtimeID"`

	// the optional `env` string tag that set with the tracer
	env string `msgp:"env,omitempty"`

	// the optional string hostname of where the tracer is running
	hostname string `msgp:"hostname,omitempty"`

	// the optional string `version` tag for the application set in the tracer
	appVersion string `msgp:"appVersion,omitempty"`

	// a collection of key to value pairs common in all `chunks`
	attributes keyValueList `msgp:"attributes,omitempty"`

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
// - int64
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
	IntValueType                // int64 -- 4
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

// newPayloadV1 returns a ready to use payloadV1.
func newPayloadV1() *payloadV1 {
	return &payloadV1{
		protocolVersion: traceProtocolV1,
		attributes:      keyValueList{},
		chunks:          make([]traceChunk, 0),
		strings:         newStringTable(),
		header:          make([]byte, 8),
		off:             8,
	}
}

func newStringTable() *stringTable {
	return &stringTable{
		strings:   []string{""},
		indices:   map[string]uint32{"": 0},
		nextIndex: 1,
	}
}

func (s *stringTable) Add(str string) {
	if _, ok := s.indices[str]; ok {
		return
	}
	s.indices[str] = s.nextIndex
	s.strings = append(s.strings, str)
	s.nextIndex += 1
}

// push pushes a new item into the stream.
func (p *payloadV1) push(t spanList) (stats payloadStats, err error) {
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

	kv := keyValueList{
		{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: p.containerID}},     // containerID
		{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: StringValueType, value: p.languageName}},    // languageName
		{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: StringValueType, value: p.languageVersion}}, // languageVersion
		{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: StringValueType, value: p.tracerVersion}},   // tracerVersion
		{key: streamingKey{isString: false, idx: 6}, value: anyValue{valueType: StringValueType, value: p.runtimeID}},       // runtimeID
		{key: streamingKey{isString: false, idx: 7}, value: anyValue{valueType: StringValueType, value: p.env}},             // env
		{key: streamingKey{isString: false, idx: 8}, value: anyValue{valueType: StringValueType, value: p.hostname}},        // hostname
		{key: streamingKey{isString: false, idx: 9}, value: anyValue{valueType: StringValueType, value: p.appVersion}},      // appVersion
	}

	tc := traceChunk{
		priority:   int32(priority),
		origin:     origin,
		attributes: keyValueList{},
		spans:      t,
		traceID:    t[0].Context().traceID[:],
	}
	p.chunks = append(p.chunks, tc)
	wr := msgp.NewWriter(&p.buf)

	err = tc.EncodeMsg(wr, p)
	if err != nil {
		return payloadStats{}, err
	}

	// once we've encoded the spans, we can encode the attributes
	kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 10}, value: anyValue{valueType: keyValueListType, value: p.attributes}}) // attributes
	err = kv.EncodeMsg(wr, p)
	if err == nil {
		err = wr.Flush()
	}

	p.recordItem()
	return p.stats(), err
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
	if p.off < len(p.header) {
		// reading header
		n = copy(b, p.header[p.off:])
		p.off += n
		return n, nil
	}
	if p.reader == nil {
		p.reader = bytes.NewReader(p.buf.Bytes())
	}
	return p.reader.Read(b)
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
		return e.WriteInt64(a.value.(int64))
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
	for _, k := range kv {
		err = k.EncodeMsg(e, p)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *traceChunk) EncodeMsg(e *msgp.Writer, p *payloadV1) error {
	e.WriteInt32(11) // write msgp index for `chunks`

	kv := keyValueList{
		{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: int64(t.priority)}},      // priority
		{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: t.origin}},            // origin
		{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: BoolValueType, value: t.droppedTrace}},        // droppedTrace
		{key: streamingKey{isString: false, idx: 6}, value: anyValue{valueType: BytesValueType, value: t.traceID}},            // traceID
		{key: streamingKey{isString: false, idx: 7}, value: anyValue{valueType: StringValueType, value: t.samplingMechanism}}, // samplingMechanism
	}

	attr := keyValueList{}
	for k, v := range t.attributes {
		attr = append(attr, keyValue{key: streamingKey{isString: false, idx: uint32(k)}, value: anyValue{valueType: getAnyValueType(v), value: v}})
	}
	kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 3}, value: anyValue{valueType: keyValueListType, value: attr}}) // attributes

	err := kv.EncodeMsg(e, p)
	if err != nil {
		return err
	}

	return EncodeSpanList(t.spans, e, p)
}

func EncodeSpanList(s spanList, e *msgp.Writer, p *payloadV1) error {
	e.WriteInt32(4) // write msgp index for `spans`

	err := e.WriteArrayHeader(uint32(len(s)))
	if err != nil {
		return msgp.WrapError(err)
	}

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
		{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: IntValueType, value: int64(s.spanID)}},   // spanID
		{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: IntValueType, value: int64(s.parentID)}}, // parentID
		{key: streamingKey{isString: false, idx: 6}, value: anyValue{valueType: IntValueType, value: int64(s.start)}},    // start
		{key: streamingKey{isString: false, idx: 7}, value: anyValue{valueType: IntValueType, value: int64(s.duration)}}, // duration
		{key: streamingKey{isString: false, idx: 8}, value: anyValue{valueType: BoolValueType, value: (s.error != 0)}},   // error - true if span has error
		{key: streamingKey{isString: false, idx: 10}, value: anyValue{valueType: StringValueType, value: s.spanType}},    // type
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

	kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 9}, value: anyValue{valueType: keyValueListType, value: attr}}) // attributes

	env, ok := s.meta["env"]
	if ok {
		kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 13}, value: anyValue{valueType: StringValueType, value: env}}) // env
	}
	version, ok := s.meta["version"]
	if ok {
		kv = append(kv, keyValue{key: streamingKey{isString: false, idx: 14}, value: anyValue{valueType: StringValueType, value: version}}) // version
	}

	err := kv.EncodeMsg(e, p)
	if err != nil {
		return err
	}

	// spanLinks
	err = encodeSpanLinks(s.spanLinks, e, p)
	if err != nil {
		return err
	}

	// spanEvents
	return encodeSpanEvents(s.spanEvents, e, p)
}

// encodeString and decodeString handles encoding a string to the payload's string table.
// When writing a string:
// - use its index in the string table if it exists
// - otherwise, write the string into the message, then add the string at the next index
// Returns the index of the string in the string table, and an error if there is one
func (p *payloadV1) encodeString(s string) (streamingKey, error) {
	sTable := p.strings
	idx, ok := sTable.indices[s]
	// if the string already exists in the table, use its index
	if ok {
		return streamingKey{isString: false, idx: idx}, nil
	}

	// else, write the string into the table at the next index
	// return an error to indicate that the string should be written to the msgp message
	sTable.Add(s)
	return streamingKey{isString: true, stringValue: s}, nil
}

// encodeSpanLinks encodes the span links into a msgp.Writer
// Span links are represented as an array of fixmaps (keyValueList)
func encodeSpanLinks(sl []SpanLink, e *msgp.Writer, p *payloadV1) error {
	err := e.WriteInt32(11) // spanLinks
	if err != nil {
		return err
	}

	// write the number of span links
	err = e.WriteArrayHeader(uint32(len(sl)))
	if err != nil {
		return err
	}

	// represent each span link as a fixmap (keyValueList) and add it to an array
	kv := arrayValue{}
	for _, s := range sl {
		slKeyValues := keyValueList{
			{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: int64(s.TraceID)}}, // traceID
			{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: IntValueType, value: int64(s.SpanID)}},  // spanID
			{key: streamingKey{isString: false, idx: 4}, value: anyValue{valueType: StringValueType, value: s.Tracestate}},  // tracestate
			{key: streamingKey{isString: false, idx: 5}, value: anyValue{valueType: IntValueType, value: int64(s.Flags)}},   // flags
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
	err := e.WriteInt32(12) // spanEvents
	if err != nil {
		return err
	}

	// write the number of span events
	err = e.WriteArrayHeader(uint32(len(se)))
	if err != nil {
		return err
	}

	// represent each span event as a fixmap (keyValueList) and add it to an array
	kv := arrayValue{}
	for _, s := range se {
		slKeyValues := keyValueList{
			{key: streamingKey{isString: false, idx: 1}, value: anyValue{valueType: IntValueType, value: int64(s.TimeUnixNano)}}, // time
			{key: streamingKey{isString: false, idx: 2}, value: anyValue{valueType: StringValueType, value: s.Name}},             // name
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

func (p *payloadV1) Decode(b []byte) ([]byte, error) {
	if p.strings == nil {
		p.strings = newStringTable()
	}

	fields, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return o, err
	}

	for fields > 0 {
		fields--

		f, o, err := msgp.ReadInt32Bytes(o)
		if err != nil {
			return o, err
		}

		switch f {
		// we don't care for the string table, so we don't decode it
		case 2: // containerID
			p.containerID, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 3: // languageName
			p.languageName, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 4: // languageVersion
			p.languageVersion, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 5: // tracerVersion
			p.tracerVersion, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 6: // runtimeID
			p.runtimeID, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 7: // env
			p.env, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 8: // hostname
			p.hostname, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}

		case 9: // appVersion
			p.appVersion, o, err = DecodeStreamingString(o, p.strings)
			if err != nil {
				return o, err
			}
		case 10: // attributes
			p.attributes, o, err = DecodeKeyValueList(o, p.strings)
			if err != nil {
				return o, err
			}
		case 11: // chunks
			p.chunks, o, err = DecodeTraceChunks(o, p.strings)
			if err != nil {
				return o, err
			}
		}
	}
	return o, nil
}

func DecodeStringTable(b []byte, strings *stringTable) ([]byte, error) {
	len, o, err := msgp.ReadBytesHeader(b)
	if err != nil {
		return nil, err
	}

	for len > 0 {
		len--
		str, o, err := msgp.ReadStringBytes(o)
		if err != nil {
			return o, err
		}

		// if we've seen the string before, skip
		if _, ok := strings.indices[str]; ok {
			continue
		}

		strings.Add(str)
	}
	return o, nil
}

func DecodeStreamingString(b []byte, strings *stringTable) (string, []byte, error) {
	if len(b) == 0 {
		return "", nil, msgp.WrapError(nil, "expected streaming string, got EOF")
	}
	// try reading as a uint32 index
	idx, o, err := msgp.ReadUint32Bytes(b)
	if err == nil {
		return strings.strings[idx], o, nil
	}

	// else, try reading as a string, then add to the string table
	str, o, err := msgp.ReadStringBytes(o)
	if err != nil {
		return "", nil, msgp.WrapError(err, "unable to read streaming string")
	}
	strings.Add(str)
	return str, o, nil
}

func DecodeAnyValue(b []byte, strings *stringTable) (anyValue, []byte, error) {
	vType, o, err := msgp.ReadInt32Bytes(b)
	if err != nil {
		return anyValue{}, o, err
	}
	switch vType {
	case StringValueType:
		str, o, err := msgp.ReadStringBytes(o)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: StringValueType, value: str}, o, nil
	case BoolValueType:
		b, o, err := msgp.ReadBoolBytes(o)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: BoolValueType, value: b}, o, nil
	case FloatValueType:
		f, o, err := msgp.ReadFloat64Bytes(o)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: FloatValueType, value: f}, o, nil
	case IntValueType:
		i, o, err := msgp.ReadInt64Bytes(o)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: IntValueType, value: i}, o, nil
	case BytesValueType:
		b, o, err := msgp.ReadBytesBytes(o, nil)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: BytesValueType, value: b}, o, nil
	case ArrayValueType:
		len, o, err := msgp.ReadArrayHeaderBytes(o)
		if err != nil {
			return anyValue{}, o, err
		}
		arrayValue := make(arrayValue, len/2)
		for i := range len / 2 {
			arrayValue[i], o, err = DecodeAnyValue(o, strings)
			if err != nil {
				return anyValue{}, o, err
			}
		}
		return anyValue{valueType: ArrayValueType, value: arrayValue}, o, nil
	case keyValueListType:
		kv, o, err := DecodeKeyValueList(o, strings)
		if err != nil {
			return anyValue{}, o, err
		}
		return anyValue{valueType: keyValueListType, value: kv}, o, nil
	default:
		return anyValue{}, o, fmt.Errorf("invalid value type: %d", vType)
	}
}

func DecodeKeyValueList(b []byte, strings *stringTable) (keyValueList, []byte, error) {
	len, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}

	if len == 0 || len%3 != 0 {
		return nil, o, msgp.WrapError(fmt.Errorf("invalid number of items in keyValueList encoding, expected multiple of 3, got %d", len))
	}

	kv := make(keyValueList, len/3)
	for i := range len / 3 {
		len--
		key, o, err := DecodeStreamingString(o, strings)
		if err != nil {
			return nil, o, err
		}
		v, o, err := DecodeAnyValue(o, strings)
		if err != nil {
			return nil, o, err
		}
		kv[i] = keyValue{key: streamingKey{isString: true, stringValue: key}, value: anyValue{valueType: v.valueType, value: v.value}}
	}
	return kv, o, nil
}

func DecodeTraceChunks(b []byte, strings *stringTable) ([]traceChunk, []byte, error) {
	len, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}

	ret := make([]traceChunk, len)
	for i := range len {
		fields, o, err := msgp.ReadArrayHeaderBytes(o)
		if err != nil {
			return nil, o, err
		}
		tc := traceChunk{}
		for fields > 0 {
			fields--

			f, o, err := msgp.ReadUint32Bytes(o)
			if err != nil {
				return ret, o, err
			}

			switch f {
			case 1: // priority
				s, o, err := msgp.ReadInt32Bytes(o)
				if err != nil {
					return ret, o, err
				}
				tc.priority = s
			case 2: // origin
				s, o, err := msgp.ReadStringBytes(o)
				if err != nil {
					return ret, o, err
				}
				tc.origin = s
			case 3: // attributes
				kv, o, err := DecodeKeyValueList(o, strings)
				if err != nil {
					return ret, o, err
				}
				tc.attributes = kv
			case 4: // spans
				s, o, err := DecodeSpanList(o, strings)
				if err != nil {
					return ret, o, err
				}
				tc.spans = s
			case 5: // droppedTrace
				s, o, err := msgp.ReadBoolBytes(o)
				if err != nil {
					return ret, o, err
				}
				tc.droppedTrace = s
			case 6: // traceID
				s, o, err := msgp.ReadBytesBytes(o, nil)
				if err != nil {
					return ret, o, err
				}
				tc.traceID = []byte(s)
			case 7: // samplingMechanism
				s, o, err := msgp.ReadStringBytes(o)
				if err != nil {
					return ret, o, err
				}
				tc.samplingMechanism = s
			}
		}
		ret[i] = tc
	}
	return ret, o, nil
}

func DecodeSpanList(b []byte, strings *stringTable) (spanList, []byte, error) {
	len, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	ret := make([]*Span, len)
	for i := range len {
		ret[i], o, err = DecodeSpan(o, strings)
		if err != nil {
			return nil, o, err
		}
	}
	return ret, o, nil
}

func DecodeSpan(b []byte, strings *stringTable) (*Span, []byte, error) {
	sp := Span{}
	fields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return &sp, o, err
	}

	for fields > 0 {
		fields--

		f, o, err := msgp.ReadUint32Bytes(o)
		if err != nil {
			return &sp, o, err
		}

		switch f {
		case 1: // service
			st, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.service = st
		case 2: // name
			st, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.name = st
		case 3: // resource
			st, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.resource = st
		case 4: // spanID
			i, o, err := msgp.ReadInt64Bytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.spanID = uint64(i)
		case 5: // parentID
			i, o, err := msgp.ReadInt64Bytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.parentID = uint64(i)
		case 6: // start
			i, o, err := msgp.ReadInt64Bytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.start = i
		case 7: // duration
			i, o, err := msgp.ReadInt64Bytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.duration = i
		case 8: // error
			i, o, err := msgp.ReadBoolBytes(o)
			if err != nil {
				return &sp, o, err
			}
			if i {
				sp.error = 1
			} else {
				sp.error = 0
			}
		case 9: // attributes
			kv, o, err := DecodeKeyValueList(o, strings)
			if err != nil {
				return &sp, o, err
			}
			for k, v := range kv {
				key := strings.strings[k]
				sp.SetTag(key, v.value.value)
			}
		case 10: // type
			st, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.spanType = st
		case 11: // spanLinks
			sl, o, err := DecodeSpanLinks(o, strings)
			if err != nil {
				return &sp, o, err
			}
			sp.spanLinks = sl
		case 12: // spanEvents
			se, o, err := DecodeSpanEvents(o, strings)
			if err != nil {
				return &sp, o, err
			}
			sp.spanEvents = se
		case 13: // env
			s, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.SetTag(ext.Environment, s)
		case 14: // version
			s, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.setMeta(ext.Version, s)
		case 15: // component
			s, o, err := msgp.ReadStringBytes(o)
			if err != nil {
				return &sp, o, err
			}
			sp.integration = s
		}
	}
	return &sp, nil, nil
}

func DecodeSpanLinks(b []byte, strings *stringTable) ([]SpanLink, []byte, error) {
	numSpanLinks, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}

	ret := make([]SpanLink, numSpanLinks)
	for i := range numSpanLinks {
		sl := SpanLink{}
		fields, o, err := msgp.ReadMapHeaderBytes(o)
		if err != nil {
			return ret, o, err
		}
		for fields > 0 {
			fields--

			f, o, err := msgp.ReadUint32Bytes(o)
			if err != nil {
				return ret, o, err
			}

			switch f {
			case 1: // traceID
				s, o, err := msgp.ReadInt64Bytes(o)
				if err != nil {
					return ret, o, err
				}
				sl.TraceID = uint64(s)
			case 2: // spanID
				s, o, err := msgp.ReadInt64Bytes(o)
				if err != nil {
					return ret, o, err
				}
				sl.SpanID = uint64(s)
			case 3: // attributes
				kv, o, err := DecodeKeyValueList(o, strings)
				if err != nil {
					return ret, o, err
				}
				for k, v := range kv {
					key := strings.strings[k]
					s, ok := v.value.value.(string)
					if !ok {
						err := msgp.WrapError(fmt.Errorf("expected string value type for span link attributes, got %T", v.value.value))
						return ret, o, err
					}
					sl.Attributes[key] = s
				}
			case 4: // tracestate
				s, o, err := msgp.ReadStringBytes(o)
				if err != nil {
					return ret, o, err
				}
				sl.Tracestate = s
			case 5: // flags
				s, o, err := msgp.ReadUint32Bytes(o)
				if err != nil {
					return ret, o, err
				}
				sl.Flags = s
			}
		}
		ret[i] = sl
	}
	return ret, o, nil
}

func DecodeSpanEvents(b []byte, strings *stringTable) ([]spanEvent, []byte, error) {
	numSpanEvents, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	ret := make([]spanEvent, numSpanEvents)
	for i := range numSpanEvents {
		se := spanEvent{}
		fields, o, err := msgp.ReadMapHeaderBytes(o)
		if err != nil {
			return ret, o, err
		}
		for fields > 0 {
			fields--

			f, o, err := msgp.ReadUint32Bytes(o)
			if err != nil {
				return ret, o, err
			}

			switch f {
			case 1: // time
				s, o, err := msgp.ReadInt64Bytes(o)
				if err != nil {
					return ret, o, err
				}
				se.TimeUnixNano = uint64(s)
			case 2: // name
				s, o, err := msgp.ReadStringBytes(o)
				if err != nil {
					return ret, o, err
				}
				se.Name = s
			case 4: // attributes
				kv, o, err := DecodeKeyValueList(o, strings)
				if err != nil {
					return ret, o, err
				}
				for k, v := range kv {
					key := strings.strings[k]
					switch v.value.valueType {
					case StringValueType:
						se.Attributes[key] = &spanEventAttribute{
							Type:        spanEventAttributeTypeString,
							StringValue: v.value.value.(string),
						}
					case BoolValueType:
						se.Attributes[key] = &spanEventAttribute{
							Type:      spanEventAttributeTypeBool,
							BoolValue: v.value.value.(bool),
						}
					case IntValueType:
						se.Attributes[key] = &spanEventAttribute{
							Type:     spanEventAttributeTypeInt,
							IntValue: v.value.value.(int64),
						}
					case FloatValueType:
						se.Attributes[key] = &spanEventAttribute{
							Type:        spanEventAttributeTypeDouble,
							DoubleValue: v.value.value.(float64),
						}
					case ArrayValueType:
						se.Attributes[key] = &spanEventAttribute{
							Type:       spanEventAttributeTypeArray,
							ArrayValue: v.value.value.(*spanEventArrayAttribute),
						}
					default:
						err := msgp.WrapError(fmt.Errorf("unexpected value type not supported by span events: %T", v.value.value))
						return ret, o, err
					}
				}
			}
		}
		ret[i] = se
	}
	return ret, nil, nil
}
