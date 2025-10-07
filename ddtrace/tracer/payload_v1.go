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
	// bm keeps track of which fields have been set in the payload
	// bits 1-11 are used for field IDs 1-11. Bit 0 is unused.
	bm bitmap

	// the string ID of the container where the tracer is running
	containerID string // 2

	// the string language name of the tracer
	languageName string // 3

	// the string language version of the tracer
	languageVersion string // 4

	// the string version of the tracer
	tracerVersion string // 5

	// the V4 string UUID representation of a tracer session
	runtimeID string // 6

	// the optional `env` string tag that set with the tracer
	env string // 7

	// the optional string hostname of where the tracer is running
	hostname string // 8

	// the optional string `version` tag for the application set in the tracer
	appVersion string // 9

	// a collection of key to value pairs common in all `chunks`
	attributes map[string]anyValue // 10

	// a list of trace `chunks`
	chunks []traceChunk // 11

	// protocolVersion specifies the trace protocol to use.
	protocolVersion float64

	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// readOff specifies the current read position on the header.
	readOff int

	// writeOff specifies the current read position on the header.
	writeOff int

	// count specifies the number of items (traceChunks) in the stream.
	count uint32

	// fields specifies the number of fields in the payload.
	fields uint32

	// buf holds the sequence of msgpack-encoded items.
	buf []byte

	// reader is used for reading the contents of buf.
	reader *bytes.Reader
}

// newPayloadV1 returns a ready to use payloadV1.
func newPayloadV1() *payloadV1 {
	return &payloadV1{
		protocolVersion: traceProtocolV1,
		attributes:      make(map[string]anyValue),
		chunks:          make([]traceChunk, 0),
		readOff:         8,
		writeOff:        0,
	}
}

// push pushes a new item (a traceChunk)into the payload.
func (p *payloadV1) push(t spanList) (stats payloadStats, err error) {
	// We need to hydrate the payload with everything we get from the spans.
	// Conceptually, our `t spanList` corresponds to one `traceChunk`.
	attributes := map[string]anyValue{}
	for _, span := range t {
		if span == nil {
			continue
		}
		for k, v := range span.meta {
			av := anyValue{valueType: StringValueType, value: v}
			attributes[k] = av
			p.attributes[k] = av
		}
		for k, v := range span.metrics {
			av := anyValue{valueType: FloatValueType, value: v}
			attributes[k] = av
			p.attributes[k] = av
		}
		// TODO(hannahkm): :sad-clown: :dead-tired:
		// for k, v := range span.metaStruct {
		// 	attributes = append(attributes, keyValue{key: k, value: anyValue{valueType: keyValueListType, value: v}})
		// }
	}

	tc := traceChunk{
		spans:   t,
		traceID: t[0].Context().traceID[:],
	}

	// if there are attributes available, set them in our bitmap and increment
	// the number of fields.
	if len(attributes) > 0 {
		tc.attributes = attributes
		p.bm.set(10)
		p.fields += 1
	}
	p.chunks = append(p.chunks, tc)
	p.recordItem()
	return p.stats(), err
}

// grows the buffer to fit n more bytes. Follows the internal Go standard
// for growing slices (https://github.com/golang/go/blob/master/src/runtime/slice.go#L289)
func (p *payloadV1) grow(n int) {
	cap := cap(p.buf)
	newLen := len(p.buf) + n
	threshold := 256
	for {
		cap += (cap + 3*threshold) >> 2
		if cap >= newLen {
			break
		}
	}
	newBuffer := make([]byte, cap)
	copy(newBuffer, p.buf)
	p.buf = newBuffer
}

func (p *payloadV1) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

func (p *payloadV1) clear() {
	p.bm = 0
	p.buf = p.buf[:]
	p.reader = nil
}

func (p *payloadV1) recordItem() {
	atomic.AddUint32(&p.count, 1)
	// p.updateHeader() TODO(hannahkm): figure out if we need this
}

func (p *payloadV1) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: p.itemCount(),
	}
}

func (p *payloadV1) size() int {
	return len(p.buf) + len(p.header) - p.readOff
}

func (p *payloadV1) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

func (p *payloadV1) protocol() float64 {
	return p.protocolVersion
}

func (p *payloadV1) updateHeader() {
	n := uint64(p.fields)
	switch {
	case n <= 15:
		p.header[7] = msgpackMapFix + byte(n)
		p.readOff = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(p.header, n) // writes 2 bytes
		p.header[5] = msgpackMap16
		p.readOff = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(p.header, n) // writes 4 bytes
		p.header[3] = msgpackMap32
		p.readOff = 3
	}
}

func (p *payloadV1) Close() error {
	p.clear()
	return nil
}

func (p *payloadV1) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	return len(b), nil
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payloadV1) Read(b []byte) (n int, err error) {
	if len(p.header) == 0 {
		p.header = make([]byte, 8)
		p.updateHeader()
	}
	if p.readOff < len(p.header) {
		// reading header
		n = copy(b, p.header[p.readOff:])
		p.readOff += n
		return n, nil
	}
	if len(p.buf) == 0 {
		p.encode()
	}
	if p.reader == nil {
		p.reader = bytes.NewReader(p.buf)
	}
	return p.reader.Read(b)
}

// encode writes existing payload fields into the buffer in msgp format.
func (p *payloadV1) encode() {
	st := newStringTable()
	p.encodeField(p.bm, 2, anyValue{valueType: StringValueType, value: p.containerID}, st)
	p.encodeField(p.bm, 3, anyValue{valueType: StringValueType, value: p.languageName}, st)
	p.encodeField(p.bm, 4, anyValue{valueType: StringValueType, value: p.languageVersion}, st)
	p.encodeField(p.bm, 5, anyValue{valueType: StringValueType, value: p.tracerVersion}, st)
	p.encodeField(p.bm, 6, anyValue{valueType: StringValueType, value: p.runtimeID}, st)
	p.encodeField(p.bm, 7, anyValue{valueType: StringValueType, value: p.env}, st)
	p.encodeField(p.bm, 8, anyValue{valueType: StringValueType, value: p.hostname}, st)
	p.encodeField(p.bm, 9, anyValue{valueType: StringValueType, value: p.appVersion}, st)

	if len(p.attributes) > 0 {
		p.encodeAttributes(10, p.attributes, st)
	}

	if len(p.chunks) > 0 {
		p.encodeTraceChunks(11, p.chunks, st)
	}
}

// TODO(hannahkm): is this the best way to go about encoding fields?
func (p *payloadV1) encodeField(bm bitmap, fieldID int, a anyValue, st *stringTable) {
	if !bm.contains(uint32(fieldID)) {
		return
	}
	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID)) // msgp key
	// p.buf = msgp.AppendInt32(p.buf, int32(a.valueType)) // value type TODO(hannahkm): do we need this?
	if a.valueType == StringValueType {
		value := a.value.(string)
		// encode msgp value, either by pulling from string table or writing it directly
		if idx, ok := st.Get(value); ok {
			p.buf = idx.encode(p.buf)
		} else {
			s := stringValue(value)
			p.buf = s.encode(p.buf)
			st.Add(value)
		}
		return
	}
	switch a.valueType {
	case BoolValueType:
		p.buf = msgp.AppendBool(p.buf, a.value.(bool))
	case FloatValueType:
		p.buf = msgp.AppendFloat64(p.buf, a.value.(float64))
	case IntValueType:
		p.buf = msgp.AppendInt64(p.buf, a.value.(int64))
	case BytesValueType:
		p.buf = msgp.AppendBytes(p.buf, a.value.([]byte))
	case ArrayValueType:
		p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(a.value.(arrayValue))))
		for _, v := range a.value.(arrayValue) {
			v.encode(p.buf)
		}
	}
}

func (p *payloadV1) encodeAttributes(fieldID int, kv map[string]anyValue, st *stringTable) error {
	if !p.bm.contains(uint32(fieldID)) || len(kv) == 0 {
		return nil
	}

	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))    // msgp key
	p.buf = msgp.AppendMapHeader(p.buf, uint32(len(kv))) // number of item pairs in map
	for k, v := range kv {
		// encode msgp key
		if idx, ok := st.Get(string(k)); ok {
			p.buf = idx.encode(p.buf)
		} else {
			p.buf = stringValue(k).encode(p.buf)
			st.Add(string(k))
		}

		// encode value
		p.buf = v.encode(p.buf)
	}
	return nil
}

// TODO(hannahkm): this references chunk.bm, which is not implemented yet
func (p *payloadV1) encodeTraceChunks(fieldID int, tc []traceChunk, st *stringTable) error {
	if len(tc) == 0 {
		return nil
	}

	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))      // msgp key
	p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(tc))) // number of chunks
	for _, chunk := range tc {
		p.buf = msgp.AppendMapHeader(p.buf, uint32(chunk.fields)) // number of item pairs in map

		// priority
		p.encodeField(chunk.bm, 1, anyValue{valueType: IntValueType, value: chunk.priority}, st)

		// origin
		p.encodeField(chunk.bm, 2, anyValue{valueType: StringValueType, value: chunk.origin}, st)

		// attributes
		if chunk.bm.contains(3) {
			p.encodeAttributes(3, chunk.attributes, st)
		}

		// spans
		if chunk.bm.contains(4) {
			p.encodeSpans(4, chunk.spans, st)
		}

		// droppedTrace
		p.encodeField(chunk.bm, 5, anyValue{valueType: BoolValueType, value: chunk.droppedTrace}, st)

		// traceID
		p.encodeField(chunk.bm, 6, anyValue{valueType: BytesValueType, value: chunk.traceID}, st)

		// samplingMechanism
		// TODO(hannahkm): I think the RFC changed, need to double check this
		p.encodeField(chunk.bm, 7, anyValue{valueType: StringValueType, value: chunk.samplingMechanism}, st)
	}

	return nil
}

func (p *payloadV1) encodeSpans(fieldID int, spans spanList, st *stringTable) error {
	if len(spans) == 0 || !p.bm.contains(uint32(fieldID)) {
		return nil
	}

	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))         // msgp key
	p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(spans))) // number of spans

	for _, span := range spans {
		if span == nil {
			continue
		}
		// TODO(hannahkm): how do we get the number of set fields efficiently?
		// TODO(hannahkm): might need to change the bitmap value in the calls below
		p.encodeField(p.bm, 1, anyValue{valueType: StringValueType, value: span.service}, st)
		p.encodeField(p.bm, 2, anyValue{valueType: StringValueType, value: span.name}, st)
		p.encodeField(p.bm, 3, anyValue{valueType: StringValueType, value: span.resource}, st)
		p.encodeField(p.bm, 4, anyValue{valueType: IntValueType, value: span.spanID}, st)
		p.encodeField(p.bm, 5, anyValue{valueType: IntValueType, value: span.parentID}, st)
		p.encodeField(p.bm, 6, anyValue{valueType: IntValueType, value: span.start}, st)
		p.encodeField(p.bm, 7, anyValue{valueType: IntValueType, value: span.duration}, st)
		if span.error != 0 {
			p.encodeField(p.bm, 8, anyValue{valueType: BoolValueType, value: true}, st)
		} else {
			p.encodeField(p.bm, 8, anyValue{valueType: BoolValueType, value: false}, st)
		}
		p.encodeField(p.bm, 10, anyValue{valueType: StringValueType, value: span.spanType}, st)
		p.encodeSpanLinks(11, span.spanLinks, st)
		p.encodeSpanEvents(12, span.spanEvents, st)
		p.encodeField(p.bm, 15, anyValue{valueType: StringValueType, value: span.integration}, st)

		// TODO(hannahkm): add attributes, env, version
	}
	return nil
}

func (p *payloadV1) encodeSpanLinks(fieldID int, spanLinks []SpanLink, st *stringTable) error {
	if len(spanLinks) == 0 || !p.bm.contains(uint32(fieldID)) {
		return nil
	}
	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))             // msgp key
	p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(spanLinks))) // number of span links

	for _, link := range spanLinks {
		// TODO(hannahkm): how do we get the number of set fields
		// TODO(hannahkm): might need to change the bitmap value in the calls below
		p.encodeField(p.bm, 1, anyValue{valueType: BytesValueType, value: link.TraceID}, st)
		p.encodeField(p.bm, 2, anyValue{valueType: IntValueType, value: link.SpanID}, st)
		p.encodeField(p.bm, 4, anyValue{valueType: StringValueType, value: link.Tracestate}, st)
		p.encodeField(p.bm, 5, anyValue{valueType: StringValueType, value: link.Flags}, st)

		// TODO(hannahkm): add attributes
	}
	return nil
}

func (p *payloadV1) encodeSpanEvents(fieldID int, spanEvents []spanEvent, st *stringTable) error {
	if len(spanEvents) == 0 || !p.bm.contains(uint32(fieldID)) {
		return nil
	}
	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))              // msgp key
	p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(spanEvents))) // number of span events

	for _, event := range spanEvents {
		// TODO(hannahkm): how do we get the number of set fields
		// TODO(hannahkm): might need to change the bitmap value in the calls below
		p.encodeField(p.bm, 1, anyValue{valueType: IntValueType, value: event.TimeUnixNano}, st)
		p.encodeField(p.bm, 2, anyValue{valueType: StringValueType, value: event.Name}, st)
		// TODO(hannahkm): add attributes
	}
	return nil
}

// Getters for payloadV1 fields
func (p *payloadV1) GetContainerID() string             { return p.containerID }
func (p *payloadV1) GetLanguageName() string            { return p.languageName }
func (p *payloadV1) GetLanguageVersion() string         { return p.languageVersion }
func (p *payloadV1) GetTracerVersion() string           { return p.tracerVersion }
func (p *payloadV1) GetRuntimeID() string               { return p.runtimeID }
func (p *payloadV1) GetEnv() string                     { return p.env }
func (p *payloadV1) GetHostname() string                { return p.hostname }
func (p *payloadV1) GetAppVersion() string              { return p.appVersion }
func (p *payloadV1) GetAttributes() map[string]anyValue { return p.attributes }

func (p *payloadV1) SetContainerID(v string) {
	p.containerID = v
	p.bm.set(2)
	p.fields += 1
}

func (p *payloadV1) SetLanguageName(v string) {
	p.languageName = v
	p.bm.set(3)
	p.fields += 1
}

func (p *payloadV1) SetLanguageVersion(v string) {
	p.languageVersion = v
	p.bm.set(4)
	p.fields += 1
}

func (p *payloadV1) SetTracerVersion(v string) {
	p.tracerVersion = v
	p.bm.set(5)
	p.fields += 1
}

func (p *payloadV1) SetRuntimeID(v string) {
	p.runtimeID = v
	p.bm.set(6)
	p.fields += 1
}

func (p *payloadV1) SetEnv(v string) {
	p.env = v
	p.bm.set(7)
	p.fields += 1
}

func (p *payloadV1) SetHostname(v string) {
	p.hostname = v
	p.bm.set(8)
	p.fields += 1
}

func (p *payloadV1) SetAppVersion(v string) {
	p.appVersion = v
	p.bm.set(9)
	p.fields += 1
}

// decodeBuffer takes the buffer from the payload, decodes it, and populates the fields
// according to the msgpack-encoded byte stream.
func (p *payloadV1) decodeBuffer() ([]byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(p.buf)
	if err != nil {
		return o, err
	}
	p.buf = o
	p.fields = numFields
	p.header = make([]byte, 8)
	p.updateHeader()

	st := newStringTable()
	for {
		// read msgp field ID
		var idx uint32
		idx, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			break
		}

		// handle attributes
		if idx == 10 {
			p.attributes, o, err = DecodeKeyValueList(o, st)
			if err != nil {
				break
			}
			continue
		}

		// handle trace chunks
		if idx == 11 {
			// p.chunks, o, err = DecodeTraceChunks(o, st)
			// if err != nil {
			// 	break
			// }
			continue
		}

		// read msgp string value
		var value string
		var ok bool
		value, o, ok = st.Read(o)
		if !ok {
			err = fmt.Errorf("unable to read string value of field %d", idx)
			break
		}

		switch idx {
		case 2:
			p.containerID = value
		case 3:
			p.languageName = value
		case 4:
			p.languageVersion = value
		case 5:
			p.tracerVersion = value
		case 6:
			p.runtimeID = value
		case 7:
			p.env = value
		case 8:
			p.hostname = value
		case 9:
			p.appVersion = value
		default:
			err = fmt.Errorf("unexpected field ID %d", idx)
		}
		if len(o) == 0 || err != nil {
			break
		}
	}
	return o, err
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

func (a anyValue) encode(buf []byte) []byte {
	buf = msgp.AppendInt32(buf, int32(a.valueType))
	switch a.valueType {
	case StringValueType:
		buf = a.value.(stringValue).encode(buf)
	case BoolValueType:
		buf = msgp.AppendBool(buf, a.value.(bool))
	case FloatValueType:
		buf = msgp.AppendFloat64(buf, a.value.(float64))
	case IntValueType:
		buf = msgp.AppendInt64(buf, a.value.(int64))
	case BytesValueType:
		buf = msgp.AppendBytes(buf, a.value.([]byte))
	case ArrayValueType:
		buf = msgp.AppendArrayHeader(buf, uint32(len(a.value.(arrayValue))))
		for _, v := range a.value.(arrayValue) {
			v.encode(buf)
		}
	}
	return buf
}

type arrayValue []anyValue

// keeps track of which fields have been set in the payload, with a
// 1 for represented fields and 0 for unset fields.
type bitmap int16

func (b *bitmap) set(bit uint32) {
	if bit >= 16 {
		return
	}
	*b |= 1 << bit
}

func (b bitmap) contains(bit uint32) bool {
	if bit >= 16 {
		return false
	}
	return b&(1<<bit) != 0
}

// an encodable and decodable index of a string in the string table
type index int32

func (i index) encode(buf []byte) []byte {
	return msgp.AppendUint32(buf, uint32(i))
}

func (i *index) decode(buf []byte) ([]byte, error) {
	val, o, err := msgp.ReadUint32Bytes(buf)
	if err != nil {
		return o, err
	}
	*i = index(val)
	return o, nil
}

// an encodable and decodable string value
type stringValue string

func (s stringValue) encode(buf []byte) []byte {
	return msgp.AppendString(buf, string(s))
}

func (s *stringValue) decode(buf []byte) ([]byte, error) {
	val, o, err := msgp.ReadStringBytes(buf)
	if err != nil {
		return o, err
	}
	*s = stringValue(val)
	return o, nil
}

type stringTable struct {
	strings   []string         // list of strings
	indices   map[string]index // map strings to their indices
	nextIndex index            // last index of the stringTable
}

func newStringTable() *stringTable {
	return &stringTable{
		strings:   []string{""},
		indices:   map[string]index{"": 0},
		nextIndex: 1,
	}
}

func (s *stringTable) Add(str string) (idx index) {
	if _, ok := s.indices[str]; ok {
		return s.indices[str]
	}
	s.indices[str] = s.nextIndex
	s.strings = append(s.strings, str)
	idx = s.nextIndex
	s.nextIndex += 1
	return
}

func (s *stringTable) Get(str string) (index, bool) {
	if idx, ok := s.indices[str]; ok {
		return idx, true
	}
	return -1, false
}

func (s *stringTable) Read(b []byte) (string, []byte, bool) {
	sType := getStreamingType(b[0])
	if sType == -1 {
		return "", b, false
	}
	// if b is a string
	if sType == 0 {
		var sv stringValue
		o, err := sv.decode(b)
		if err != nil {
			return "", o, false
		}
		str := string(sv)
		s.Add(str)
		return str, o, true
	}
	// if b is an index
	var i index
	o, err := i.decode(b)
	if err != nil {
		return "", o, false
	}
	return s.strings[i], o, true
}

// returns 0 if the given byte is a string,
// 1 if it is an int32, and -1 if it is neither.
func getStreamingType(b byte) int {
	switch b {
	// String formats
	case 0xd9, 0xda, 0xdb: // str8, str16, str32
		return 0
	case 0xce: // uint32
		return 1
	default:
		// Check for fixstr
		if b&0xe0 == 0xa0 {
			return 0
		}
		// Check for positive fixint
		if b&0x80 == 0 {
			return 1
		}
		return -1
	}
}

// traceChunk represents a list of spans with the same trace ID,
// i.e. a chunk of a trace
type traceChunk struct {
	// bitmap to track which fields have been set in the trace chunk
	bm bitmap

	// number of fields in the trace chunk
	fields uint32

	// the sampling priority of the trace
	priority int32

	// the optional string origin ("lambda", "rum", etc.) of the trace chunk
	origin string

	// a collection of key to value pairs common in all `spans`
	attributes map[string]anyValue

	// a list of spans in this chunk
	spans spanList

	// whether the trace only contains analyzed spans
	// (not required by tracers and set by the agent)
	droppedTrace bool

	// the ID of the trace to which all spans in this chunk belong
	traceID []byte

	// the optional string decision maker (previously span tag _dd.p.dm)
	samplingMechanism string
}

// Decoding Functions

func DecodeAnyValue(b []byte, strings *stringTable) (anyValue, []byte, error) {
	vType, o, err := msgp.ReadInt32Bytes(b)
	if err != nil {
		return anyValue{}, o, err
	}
	switch vType {
	case StringValueType:
		str, o, ok := strings.Read(o)
		if !ok {
			return anyValue{}, o, fmt.Errorf("unable to read string value")
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

func DecodeKeyValueList(b []byte, strings *stringTable) (map[string]anyValue, []byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}

	kv := map[string]anyValue{}
	for i := range numFields {
		key, o, ok := strings.Read(o)
		if !ok {
			return nil, o, fmt.Errorf("unable to read key of field %d", i)
		}
		value, o, err := DecodeAnyValue(o, strings)
		if err != nil {
			return nil, o, err
		}
		kv[key] = value
	}
	return kv, o, nil
}
