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
	"unique"

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
	strings *stringTable

	// fieldSet tracks which index fields are set (bits 1-11 for field IDs 1-11)
	// Bit 0 is unused since field IDs start from 1
	fieldSet bitmap

	// Array to store the actual index values for set fields
	// Index in array corresponds to field ID (fieldValues[1] = containerID value, etc.)
	fieldValues [12]index // 12 to accommodate indices 1-11, index 0 unused

	// a collection of key to value pairs common in all `chunks`
	attributes keyValueList

	// a list of trace `chunks`
	chunks []traceChunk

	// protocolVersion specifies the trace protocol to use.
	protocolVersion float64

	// header specifies the first few bytes in the msgpack stream
	// indicating the type of array (fixarray, array16 or array32)
	// and the number of items contained in the stream.
	header []byte

	// off specifies the current read position on the header.
	off int

	// writeOff specifies the current write position on the header.
	writeOff int

	// count specifies the number of items in the stream.
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
		attributes:      keyValueList{},
		chunks:          make([]traceChunk, 0),
		strings:         newStringTable(),
		off:             8,
		writeOff:        0,
		fields:          1,
	}
}

// push pushes a new item into the stream.
func (p *payloadV1) push(t spanList) (stats payloadStats, err error) {
	tc := traceChunk{
		spans: t,
	}
	p.chunks = append(p.chunks, tc)

	p.recordItem()
	return p.stats(), err
}

func (p *payloadV1) grow(n int) {
	c := cap(p.buf) - len(p.buf)
	// if n fits in current available capacity, don't allocate
	if n <= c {
		return
	}
	// allocating 1.5 times what's needed, to reduce allocations
	m := n + len(p.buf)
	buf := make([]byte, (m+1)*3/2)
	copy(buf, p.buf)
	p.buf = buf
}

func (p *payloadV1) reset() {
	p.updateHeader()
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

func (p *payloadV1) clear() {
	p.fields = 0
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
	return len(p.buf) + len(p.header) - p.off
}

func (p *payloadV1) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

func (p *payloadV1) protocol() float64 {
	return p.protocolVersion
}

// updateHeader updates the payload header based on the number of items currently
// present in the stream.
func (p *payloadV1) updateHeader() {
	n := uint64(p.fields)
	switch {
	case n <= 15:
		p.header[7] = msgpackMapFix + byte(n)
		p.off = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(p.header, n) // writes 2 bytes
		p.header[5] = msgpackMap16
		p.off = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(p.header, n) // writes 4 bytes
		p.header[3] = msgpackMap32
		p.off = 3
	}
}

// Read implements io.Reader. It reads from the msgpack-encoded stream.
func (p *payloadV1) Read(b []byte) (int, error) {
	var n int
	if len(p.header) == 0 {
		p.header = make([]byte, 8)
		p.updateHeader()
	}
	if p.off < len(p.header) {
		// reading header
		n = copy(b, p.header[p.off:])
		p.off += n
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

func (p *payloadV1) encode() {
	// fieldEncoded tracks which index fields have been encoded (bits 1-11 for field IDs 1-11)
	// Bit 0 is unused since field IDs start from 1.
	var fieldEncoded bitmap

	for i := uint32(2); i <= 9; i++ {
		if !p.hasIndexField(i) {
			continue
		}
		if fieldEncoded.Has(i) {
			p.buf = encodeField(p.buf, i, p.getIndexField(i))
			continue
		}
		v := encodableString(p.getStringField(i))
		p.buf = encodeField(p.buf, i, &v)
		fieldEncoded.Set(i)
	}
}

// Write implements io.Writer. It writes data directly to the internal buffers.
func (p *payloadV1) Write(data []byte) (int, error) {
	p.buf = append(p.buf, data...)
	return len(data), nil
}

func (p *payloadV1) hydrate() ([]byte, error) {
	_, data, err := msgp.ReadMapHeaderBytes(p.buf)
	if err != nil {
		return data, err
	}
	p.buf = data
	p.header = make([]byte, 8)

	o := p.buf
	for {
		var field uint32
		field, o, err = msgp.ReadUint32Bytes(o)
		if err != nil {
			break
		}
		switch field {
		case 1: // strings - unused because we are using an embedded streaming string table
			o, err = p.strings.decode(o)
		case 2: // containerID
			fallthrough
		case 3: // languageName
			fallthrough
		case 4: // languageVersion
			fallthrough
		case 5: // tracerVersion
			fallthrough
		case 6: // runtimeID
			fallthrough
		case 7: // env
			fallthrough
		case 8: // hostname
			fallthrough
		case 9: // appVersion
			switch detectStringOrUint32Format(o[0]) {
			case 0: // string
				var v encodableString
				o, err = v.decode(o)
				if err != nil {
					break
				}
				idx := p.strings.Add(string(v))
				p.setIndexField(field, idx)
			case 1: // string
				var idx index
				o, err = idx.decode(o)
				if err != nil {
					break
				}
				p.setIndexField(field, idx)
			default:
				err = fmt.Errorf("invalid type on field %d", field)
			}
		case 10, 11:
			// TODO: implement remaining fields
		default:
			err = fmt.Errorf("unknown field %d", field)
		}
		if len(o) == 0 || err != nil {
			break
		}
	}
	return o, err
}

// Close implements io.Closer
func (p *payloadV1) Close() error {
	p.clear()
	return nil
}

// getIndexField returns the index value for the given field ID if it's set, nil otherwise
func (p *payloadV1) getIndexField(fieldID uint32) *index {
	if fieldID == 0 || fieldID >= 12 {
		return nil
	}
	if p.fieldSet.Has(fieldID) {
		return &p.fieldValues[fieldID]
	}
	return nil
}

// setIndexField sets the index value for the given field ID
func (p *payloadV1) setIndexField(fieldID uint32, value index) {
	if fieldID == 0 || fieldID >= 12 {
		return
	}
	p.fieldValues[fieldID] = value
	p.fieldSet.Set(fieldID)
	p.fields++
}

// hasIndexField checks if a field is set using bitwise operations
func (p *payloadV1) hasIndexField(fieldID uint32) bool {
	if fieldID == 0 || fieldID >= 12 {
		return false
	}
	return p.fieldSet.Has(fieldID)
}

// getStringField returns the string value for the given field ID by looking up the index in the string table
func (p *payloadV1) getStringField(fieldID uint32) string {
	if fieldID == 0 || fieldID >= 12 {
		return ""
	}
	if !p.fieldSet.Has(fieldID) {
		return ""
	}
	idx := p.fieldValues[fieldID]
	if int(idx) < len(p.strings.strings) {
		return p.strings.strings[idx]
	}
	return ""
}

// Field accessors for backward compatibility - these delegate to the bitmap
func (p *payloadV1) ContainerID() string     { return p.getStringField(2) }
func (p *payloadV1) LanguageName() string    { return p.getStringField(3) }
func (p *payloadV1) LanguageVersion() string { return p.getStringField(4) }
func (p *payloadV1) TracerVersion() string   { return p.getStringField(5) }
func (p *payloadV1) RuntimeID() string       { return p.getStringField(6) }
func (p *payloadV1) Env() string             { return p.getStringField(7) }
func (p *payloadV1) Hostname() string        { return p.getStringField(8) }
func (p *payloadV1) AppVersion() string      { return p.getStringField(9) }

func (p *payloadV1) SetContainerID(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(2, idx)
}

func (p *payloadV1) SetLanguageName(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(3, idx)
}

func (p *payloadV1) SetLanguageVersion(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(4, idx)
}

func (p *payloadV1) SetTracerVersion(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(5, idx)
}

func (p *payloadV1) SetRuntimeID(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(6, idx)
}

func (p *payloadV1) SetEnv(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(7, idx)
}

func (p *payloadV1) SetHostname(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(8, idx)
}

func (p *payloadV1) SetAppVersion(value string) {
	idx := p.strings.Add(value)
	p.setIndexField(9, idx)
}

func encodeField[T encodeDecoder](buf []byte, ref uint32, w T) []byte {
	buf = msgp.AppendUint32(buf, ref)
	buf = w.encode(buf)
	return buf
}

// detectStringOrUint32Format examines the first byte of MessagePack data
// to determine if it represents a string or uint32 format.
// Returns 0 if string, 1 if uint32, or -1 if invalid.
func detectStringOrUint32Format(firstByte byte) int8 {
	switch firstByte {
	// String formats
	case 0xd9, 0xda, 0xdb: // str8, str16, str32
		return 0
	case 0xce: // uint32
		return 1
	default:
		// Check for fixstr: high 3 bits should be 0b101 (0xa0)
		if firstByte&0xe0 == 0xa0 {
			return 0
		}
		// Check for positive fixint: high bit should be 0 (values 0-127)
		if firstByte&0x80 == 0 {
			return 1
		}
		return -1
	}
}

type encodableString string

func (es encodableString) encode(buf []byte) []byte {
	return msgp.AppendString(buf, string(es))
}

func (es *encodableString) decode(buf []byte) ([]byte, error) {
	v, o, err := msgp.ReadStringBytes(buf)
	if err != nil {
		return o, err
	}
	*es = encodableString(v)
	return o, nil
}

type encodeDecoder interface {
	encode([]byte) []byte
	decode([]byte) ([]byte, error)
}

type index uint32

func (i index) encode(buf []byte) []byte {
	return msgp.AppendUint32(buf, uint32(i))
}

func (i *index) decode(buf []byte) ([]byte, error) {
	v, o, err := msgp.ReadUint32Bytes(buf)
	if err != nil {
		return o, err
	}
	*i = index(v)
	return o, nil
}

type bitmap uint16

func (bm *bitmap) Set(i uint32) {
	*bm |= (1 << i)
}

func (bm bitmap) Has(i uint32) bool {
	return bm&(1<<i) != 0
}

type stringTable struct {
	strings   []string                        // list of strings
	indices   map[unique.Handle[string]]index // map strings to their indices
	nextIndex index                           // last index of the stringTable
}

func newStringTable() *stringTable {
	return &stringTable{
		strings: []string{""},
		indices: map[unique.Handle[string]]index{
			unique.Make(""): 0,
		},
		nextIndex: 1,
	}
}

func (s *stringTable) Add(str string) index {
	k := unique.Make(str)
	if v, ok := s.indices[k]; ok {
		return v
	}
	v := s.nextIndex
	s.indices[k] = v
	s.strings = append(s.strings, str)
	s.nextIndex += 1
	return v
}

func (st *stringTable) encode(buf []byte) []byte {
	o := msgp.AppendArrayHeader(buf, uint32(len(st.strings)))
	for ix := range st.strings {
		o = msgp.AppendString(o, st.strings[ix])
	}
	return o
}

func (st *stringTable) decode(data []byte) ([]byte, error) {
	sz, o, err := msgp.ReadArrayHeaderBytes(data)
	if err != nil {
		return nil, err
	}
	for range sz {
		var (
			s   string
			err error
		)
		s, o, err = msgp.ReadStringBytes(o)
		if err != nil {
			return nil, err
		}
		st.Add(s)
	}
	return o, nil
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

// keyValue is made up of the key and an AnyValue (the type of the value and the value itself)
// The key is either a uint32 index into the string table or a string value.
type keyValue struct {
	key   encodableString
	value anyValue
}

type keyValueList []keyValue

// traceChunk represents a list of spans with the same trace ID,
// i.e. a chunk of a trace
type traceChunk struct {
	// the sampling priority of the trace
	priority int32

	// the optional string origin ("lambda", "rum", etc.) of the trace chunk
	origin string

	// a collection of key to value pairs common in all `spans`
	attributes keyValueList

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
