// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
	if !p.bm.contains(11) && len(t) > 0 {
		p.bm.set(11)
		p.fields += 1
	}

	// For now, we blindly set the origin, priority, and attributes values for the chunk
	// In the future, attributes should hold values that are shared across all chunks in the payload
	attributes := map[string]anyValue{}
	origin, priority, sm := "", 0, 0
	for _, span := range t {
		if span == nil {
			break
		}
		if p, ok := span.Context().SamplingPriority(); ok {
			origin = span.Context().origin
			priority = p
			attributes["service"] = anyValue{valueType: StringValueType, value: span.Root().service}
			dm := span.context.trace.propagatingTag(keyDecisionMaker)
			sm, err = strconv.Atoi(dm)
			if err != nil {
				log.Error("failed to convert decision maker to int: %s", err.Error())
			}
			break
		}
	}

	tc := traceChunk{
		spans:             t,
		priority:          int32(priority),
		origin:            origin,
		traceID:           t[0].Context().traceID[:],
		samplingMechanism: uint32(sm),
	}

	// if there are attributes available, set them in our bitmap and increment
	// the number of fields.
	if !p.bm.contains(10) && len(attributes) > 0 {
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
	n := atomic.LoadUint32(&p.fields)
	switch {
	case n <= 15:
		p.header[7] = msgpackMapFix + byte(n)
		p.readOff = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(p.header, uint64(n)) // writes 2 bytes
		p.header[5] = msgpackMap16
		p.readOff = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(p.header, uint64(n)) // writes 4 bytes
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
	p.buf = msgp.AppendMapHeader(p.buf, p.fields) // number of fields in payload
	p.buf = encodeField(p.buf, p.bm, 2, p.containerID, st)
	p.buf = encodeField(p.buf, p.bm, 3, p.languageName, st)
	p.buf = encodeField(p.buf, p.bm, 4, p.languageVersion, st)
	p.buf = encodeField(p.buf, p.bm, 5, p.tracerVersion, st)
	p.buf = encodeField(p.buf, p.bm, 6, p.runtimeID, st)
	p.buf = encodeField(p.buf, p.bm, 7, p.env, st)
	p.buf = encodeField(p.buf, p.bm, 8, p.hostname, st)
	p.buf = encodeField(p.buf, p.bm, 9, p.appVersion, st)

	if len(p.attributes) > 0 {
		p.encodeAttributes(10, p.attributes, st)
	}

	if len(p.chunks) > 0 {
		p.encodeTraceChunks(11, p.chunks, st)
	}
}

type fieldValue interface {
	bool | []byte | int32 | int64 | uint32 | uint64 | string
}

// encodeField takes a field of any value and encodes it into the given buffer
// in msgp format.
func encodeField[F fieldValue](buf []byte, bm bitmap, fieldID uint32, a F, st *stringTable) []byte {
	if !bm.contains(fieldID) {
		return buf
	}
	buf = msgp.AppendUint32(buf, uint32(fieldID)) // msgp key
	switch value := any(a).(type) {
	case string:
		// encode msgp value, either by pulling from string table or writing it directly
		if idx, ok := st.Get(value); ok {
			buf = idx.encode(buf)
		} else {
			s := stringValue(value)
			buf = s.encode(buf)
			st.Add(value)
		}
	case bool:
		buf = msgp.AppendBool(buf, value)
	case float64:
		buf = msgp.AppendFloat64(buf, value)
	case int32, int64:
		buf = msgp.AppendInt64(buf, handleIntValue(value))
	case []byte:
		buf = msgp.AppendBytes(buf, value)
	case arrayValue:
		buf = msgp.AppendArrayHeader(buf, uint32(len(value)))
		for _, v := range value {
			buf = v.encode(buf)
		}
	}
	return buf
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

func (p *payloadV1) encodeTraceChunks(fieldID int, tc []traceChunk, st *stringTable) error {
	if len(tc) == 0 || !p.bm.contains(uint32(fieldID)) {
		return nil
	}

	p.buf = msgp.AppendUint32(p.buf, uint32(fieldID))      // msgp key
	p.buf = msgp.AppendArrayHeader(p.buf, uint32(len(tc))) // number of chunks
	for _, chunk := range tc {
		p.buf = msgp.AppendMapHeader(p.buf, 7) // number of fields in chunk

		// priority
		p.buf = encodeField(p.buf, fullSetBitmap, 1, chunk.priority, st)

		// origin
		p.buf = encodeField(p.buf, fullSetBitmap, 2, chunk.origin, st)

		// attributes
		p.encodeAttributes(3, chunk.attributes, st)

		// spans
		p.encodeSpans(4, chunk.spans, st)

		// droppedTrace
		p.buf = encodeField(p.buf, fullSetBitmap, 5, chunk.droppedTrace, st)

		// traceID
		p.buf = encodeField(p.buf, fullSetBitmap, 6, chunk.traceID, st)

		// samplingMechanism
		p.buf = encodeField(p.buf, fullSetBitmap, 7, chunk.samplingMechanism, st)
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
		p.buf = msgp.AppendMapHeader(p.buf, 16) // number of fields in span

		p.buf = encodeField(p.buf, fullSetBitmap, 1, span.service, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 2, span.name, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 3, span.resource, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 4, span.spanID, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 5, span.parentID, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 6, span.start, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 7, span.duration, st)
		if span.error != 0 {
			p.buf = encodeField(p.buf, fullSetBitmap, 8, true, st)
		} else {
			p.buf = encodeField(p.buf, fullSetBitmap, 8, false, st)
		}

		// span attributes combine the meta (tags), metrics and meta_struct
		attr := map[string]anyValue{}
		for k, v := range span.meta {
			attr[k] = anyValue{
				valueType: StringValueType,
				value:     stringValue(v),
			}
		}
		for k, v := range span.metrics {
			attr[k] = anyValue{
				valueType: FloatValueType,
				value:     v,
			}
		}
		for k, v := range span.metaStruct {
			av := buildAnyValue(v)
			if av != nil {
				attr[k] = *av
			}
		}
		p.encodeAttributes(9, attr, st)

		p.buf = encodeField(p.buf, fullSetBitmap, 10, span.spanType, st)
		p.encodeSpanLinks(11, span.spanLinks, st)
		p.encodeSpanEvents(12, span.spanEvents, st)

		env := span.meta[ext.Environment]
		p.buf = encodeField(p.buf, fullSetBitmap, 13, env, st)

		version := span.meta[ext.Version]
		p.buf = encodeField(p.buf, fullSetBitmap, 14, version, st)

		p.buf = encodeField(p.buf, fullSetBitmap, 15, span.integration, st)
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
		p.buf = msgp.AppendMapHeader(p.buf, 5) // number of fields in span link

		p.buf = encodeField(p.buf, fullSetBitmap, 1, link.TraceID, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 2, link.SpanID, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 4, link.Tracestate, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 5, link.Flags, st)

		attr := map[string]anyValue{}
		for k, v := range link.Attributes {
			attr[k] = anyValue{
				valueType: StringValueType,
				value:     stringValue(v),
			}
		}
		p.encodeAttributes(3, attr, st)
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
		p.buf = msgp.AppendMapHeader(p.buf, 3) // number of fields in span event

		p.buf = encodeField(p.buf, fullSetBitmap, 1, event.TimeUnixNano, st)
		p.buf = encodeField(p.buf, fullSetBitmap, 2, event.Name, st)

		attr := map[string]anyValue{}
		for k, v := range event.Attributes {
			switch v.Type {
			case spanEventAttributeTypeString:
				attr[k] = anyValue{
					valueType: StringValueType,
					value:     v.StringValue,
				}
			case spanEventAttributeTypeInt:
				attr[k] = anyValue{
					valueType: IntValueType,
					value:     handleIntValue(v.IntValue),
				}
			case spanEventAttributeTypeDouble:
				attr[k] = anyValue{
					valueType: FloatValueType,
					value:     v.DoubleValue,
				}
			case spanEventAttributeTypeBool:
				attr[k] = anyValue{
					valueType: BoolValueType,
					value:     v.BoolValue,
				}
			case spanEventAttributeTypeArray:
				attr[k] = anyValue{
					valueType: ArrayValueType,
					value:     v.ArrayValue,
				}
			default:
				log.Warn("dropped unsupported span event attribute type %d", v.Type)
			}
		}
		p.encodeAttributes(3, attr, st)
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
			p.chunks, o, err = DecodeTraceChunks(o, st)
			if err != nil {
				break
			}
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

func buildAnyValue(v any) *anyValue {
	switch v := v.(type) {
	case string:
		return &anyValue{valueType: StringValueType, value: v}
	case bool:
		return &anyValue{valueType: BoolValueType, value: v}
	case float64:
		return &anyValue{valueType: FloatValueType, value: v}
	case int32, int64:
		return &anyValue{valueType: IntValueType, value: handleIntValue(v)}
	case []byte:
		return &anyValue{valueType: BytesValueType, value: v}
	case arrayValue:
		return &anyValue{valueType: ArrayValueType, value: v}
	default:
		return nil
	}
}

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

// translate any int value to int64
func handleIntValue(a any) int64 {
	switch v := a.(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	default:
		// Fallback for other integer types
		return v.(int64)
	}
}

type arrayValue []anyValue

// keeps track of which fields have been set in the payload, with a
// 1 for represented fields and 0 for unset fields.
type bitmap int16

var fullSetBitmap bitmap = -1

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
	samplingMechanism uint32
}

// Decoding Functions
func DecodeTraceChunks(b []byte, st *stringTable) ([]traceChunk, []byte, error) {
	out := []traceChunk{}
	numChunks, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	for range numChunks {
		tc := traceChunk{}
		b, err = tc.decode(b, st)
		if err != nil {
			return nil, o, err
		}
		out = append(out, tc)
	}
	return out, o, nil
}

func (tc *traceChunk) decode(b []byte, st *stringTable) ([]byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return o, err
	}
	for range numFields {
		// read msgp field ID
		var idx uint32
		idx, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			return o, err
		}

		// read msgp string value
		switch idx {
		case 1:
			tc.priority, o, err = msgp.ReadInt32Bytes(o)
			if err != nil {
				return o, err
			}
		case 2:
			tc.origin, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 3:
			tc.attributes, o, err = DecodeKeyValueList(o, st)
			if err != nil {
				return o, err
			}
		case 4:
			tc.spans, o, err = DecodeSpans(o, st)
			if err != nil {
				return o, err
			}
		case 5:
			tc.droppedTrace, o, err = msgp.ReadBoolBytes(o)
			if err != nil {
				return o, err
			}
		case 6:
			tc.traceID, o, err = msgp.ReadBytesBytes(o, nil)
			if err != nil {
				return o, err
			}
		case 7:
			tc.samplingMechanism, o, err = msgp.ReadUint32Bytes(o)
			if err != nil {
				return o, err
			}
		default:
			return o, fmt.Errorf("unexpected field ID %d", idx)
		}
	}
	return o, err
}

func DecodeSpans(b []byte, st *stringTable) (spanList, []byte, error) {
	out := spanList{}
	numSpans, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	for range numSpans {
		span := Span{}
		b, err = span.decode(b, st)
		if err != nil {
			return nil, o, err
		}
		out = append(out, &span)
	}
	return out, o, nil
}

func (span *Span) decode(b []byte, st *stringTable) ([]byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return o, err
	}
	for range numFields {
		// read msgp field ID
		var idx uint32
		idx, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			return o, err
		}

		// read msgp string value
		switch idx {
		case 1:
			span.service, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 2:
			span.name, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 3:
			span.resource, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 4:
			span.spanID, o, err = msgp.ReadUint64Bytes(o)
			if err != nil {
				return o, err
			}
		case 5:
			span.parentID, o, err = msgp.ReadUint64Bytes(o)
			if err != nil {
				return o, err
			}
		case 6:
			span.start, o, err = msgp.ReadInt64Bytes(o)
			if err != nil {
				return o, err
			}
		case 7:
			span.duration, o, err = msgp.ReadInt64Bytes(o)
			if err != nil {
				return o, err
			}
		case 8:
			var v bool
			v, o, err = msgp.ReadBoolBytes(o)
			if err != nil {
				return o, err
			}
			if v {
				span.error = 1
			} else {
				span.error = 0
			}
		// case 9:
		// 	span.attributes, o, err = DecodeKeyValueList(o, st)
		// 	if err != nil {
		// 		return o, err
		// 	}
		case 10:
			span.spanType, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 11:
			span.spanLinks, o, err = DecodeSpanLinks(o, st)
			if err != nil {
				return o, err
			}
		case 12:
			span.spanEvents, o, err = DecodeSpanEvents(o, st)
			if err != nil {
				return o, err
			}
		case 13:
			var env string
			env, o, err = msgp.ReadStringBytes(o)
			if env != "" && err != nil {
				span.SetTag(ext.Environment, env)
			}
		case 14:
			var ver string
			ver, o, err = msgp.ReadStringBytes(o)
			if ver != "" && err != nil {
				span.SetTag(ext.Version, ver)
			}
		case 15:
			span.integration, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		default:
			return o, fmt.Errorf("unexpected field ID %d", idx)
		}
	}
	return o, err
}

func DecodeSpanLinks(b []byte, st *stringTable) ([]SpanLink, []byte, error) {
	out := []SpanLink{}
	numLinks, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	for range numLinks {
		link := SpanLink{}
		b, err = link.decode(b, st)
		if err != nil {
			return nil, o, err
		}
		out = append(out, link)
	}
	return out, o, nil
}

func (link *SpanLink) decode(b []byte, st *stringTable) ([]byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return o, err
	}
	for range numFields {
		// read msgp field ID
		var idx uint32
		idx, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			return o, err
		}

		// read msgp string value
		switch idx {
		case 1:
			link.TraceID, o, err = msgp.ReadUint64Bytes(o)
			if err != nil {
				return o, err
			}
		case 2:
			link.SpanID, o, err = msgp.ReadUint64Bytes(o)
			if err != nil {
				return o, err
			}
		// case 3:
		// 	link.Attributes, o, err = DecodeKeyValueList(o, st)
		// 	if err != nil {
		// 		return o, err
		// 	}
		case 4:
			link.Tracestate, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
		case 5:
			link.Flags, o, err = msgp.ReadUint32Bytes(o)
			if err != nil {
				return o, err
			}
		default:
			return o, fmt.Errorf("unexpected field ID %d", idx)
		}
	}
	return o, err
}

func DecodeSpanEvents(b []byte, st *stringTable) ([]spanEvent, []byte, error) {
	out := []spanEvent{}
	numEvents, o, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return nil, o, err
	}
	for range numEvents {
		event := spanEvent{}
		b, err = event.decode(b, st)
		if err != nil {
			return nil, o, err
		}
		out = append(out, event)
	}
	return out, o, nil
}

func (event *spanEvent) decode(b []byte, st *stringTable) ([]byte, error) {
	numFields, o, err := msgp.ReadMapHeaderBytes(b)
	if err != nil {
		return o, err
	}
	for range numFields {
		// read msgp field ID
		var idx uint32
		idx, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			return o, err
		}
		switch idx {
		case 1:
			event.TimeUnixNano, o, err = msgp.ReadUint64Bytes(o)
			if err != nil {
				return o, err
			}
		case 2:
			event.Name, o, err = msgp.ReadStringBytes(o)
			if err != nil {
				return o, err
			}
			// case 3:
			// 	event.Attributes, o, err = DecodeKeyValueList(o, st)
			// 	if err != nil {
			// 		break
			// 	}
		default:
			return o, fmt.Errorf("unexpected field ID %d", idx)
		}
	}
	return o, err
}

func decodeAnyValue(b []byte, strings *stringTable) (anyValue, []byte, error) {
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
		intVal := handleIntValue(i)
		return anyValue{valueType: IntValueType, value: intVal}, o, nil
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
			arrayValue[i], o, err = decodeAnyValue(o, strings)
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
		av, o, err := decodeAnyValue(o, strings)
		if err != nil {
			return nil, o, err
		}
		kv[key] = av
	}
	return kv, o, nil
}
