// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package otlpjson implements the OTLP/JSON export used only when the OTLP
// protocol is "http/json".
//
// The upstream OpenTelemetry Go exporters only produce protobuf, so for
// "http/json" dd-trace-go encodes JSON directly from the SDK records
// (sdklog.Record and metricdata.ResourceMetrics) and sends it itself.
// "http/protobuf" and "grpc" keep using the upstream exporters.
//
// The encoding follows the OTLP/JSON rules: lowerCamelCase field names,
// traceId and spanId as lowercase hex, enums as integers, 64-bit integers as
// decimal strings, bytes as base64, and zero values omitted.
package otlpjson

import (
	"encoding/base64"
	"encoding/hex"
	"math"
	"strconv"
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
)

// maxPooledBufferSize caps the buffers kept in the pool so one huge batch
// does not pin memory forever.
const maxPooledBufferSize = 4 << 20

var bufferPool = sync.Pool{New: func() any { return new([]byte) }}

// GetBuffer returns an empty buffer from the pool. Give it back with PutBuffer.
func GetBuffer() *[]byte {
	b := bufferPool.Get().(*[]byte)
	*b = (*b)[:0]
	return b
}

// PutBuffer returns b to the pool.
func PutBuffer(b *[]byte) {
	if cap(*b) <= maxPooledBufferSize {
		bufferPool.Put(b)
	}
}

// encoder appends JSON to buf. Commas are inserted by field and elem, which
// look at the last byte written: only the start of an object or array needs no
// comma, because every value ends in a quote, digit, letter, ']' or '}'.
type encoder struct {
	buf []byte
}

func (e *encoder) sep() {
	if n := len(e.buf); n > 0 && e.buf[n-1] != '{' && e.buf[n-1] != '[' {
		e.buf = append(e.buf, ',')
	}
}

// field starts the object member name.
func (e *encoder) field(name string) {
	e.sep()
	e.buf = append(e.buf, '"')
	e.buf = append(e.buf, name...)
	e.buf = append(e.buf, '"', ':')
}

// elem starts an array element.
func (e *encoder) elem() { e.sep() }

func (e *encoder) beginObject() { e.buf = append(e.buf, '{') }
func (e *encoder) endObject()   { e.buf = append(e.buf, '}') }
func (e *encoder) beginArray()  { e.buf = append(e.buf, '[') }
func (e *encoder) endArray()    { e.buf = append(e.buf, ']') }

const hexDigits = "0123456789abcdef"

// str writes s as a JSON string. Invalid UTF-8 is replaced with U+FFFD, as
// protobuf string fields cannot carry it.
func (e *encoder) str(s string) {
	e.buf = append(e.buf, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				e.buf = append(e.buf, s[start:i]...)
				e.buf = append(e.buf, "�"...)
				i += size
				start = i
				continue
			}
			i += size
			continue
		}
		if c >= 0x20 && c != '"' && c != '\\' {
			i++
			continue
		}
		e.buf = append(e.buf, s[start:i]...)
		switch c {
		case '"', '\\':
			e.buf = append(e.buf, '\\', c)
		case '\n':
			e.buf = append(e.buf, '\\', 'n')
		case '\r':
			e.buf = append(e.buf, '\\', 'r')
		case '\t':
			e.buf = append(e.buf, '\\', 't')
		default:
			e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		start = i
	}
	e.buf = append(e.buf, s[start:]...)
	e.buf = append(e.buf, '"')
}

// int64Str writes v as a quoted decimal, the OTLP/JSON form of 64-bit integers.
func (e *encoder) int64Str(v int64) {
	e.buf = append(e.buf, '"')
	e.buf = strconv.AppendInt(e.buf, v, 10)
	e.buf = append(e.buf, '"')
}

func (e *encoder) uint64Str(v uint64) {
	e.buf = append(e.buf, '"')
	e.buf = strconv.AppendUint(e.buf, v, 10)
	e.buf = append(e.buf, '"')
}

// integer writes v as a JSON number (enums and 32-bit integers).
func (e *encoder) integer(v int64) { e.buf = strconv.AppendInt(e.buf, v, 10) }

func (e *encoder) boolean(v bool) { e.buf = strconv.AppendBool(e.buf, v) }

// float writes f as a JSON number, or as the strings OTLP/JSON uses for values JSON cannot represent.
func (e *encoder) float(f float64) {
	switch {
	case math.IsNaN(f):
		e.buf = append(e.buf, `"NaN"`...)
	case math.IsInf(f, 1):
		e.buf = append(e.buf, `"Infinity"`...)
	case math.IsInf(f, -1):
		e.buf = append(e.buf, `"-Infinity"`...)
	default:
		e.buf = strconv.AppendFloat(e.buf, f, 'g', -1, 64)
	}
}

// hexID writes b as a quoted lowercase hex string (traceId, spanId).
func (e *encoder) hexID(b []byte) {
	e.buf = append(e.buf, '"')
	e.buf = hex.AppendEncode(e.buf, b)
	e.buf = append(e.buf, '"')
}

func (e *encoder) bytes(b []byte) {
	e.buf = append(e.buf, '"')
	e.buf = base64.StdEncoding.AppendEncode(e.buf, b)
	e.buf = append(e.buf, '"')
}

// optStr writes the member name only when s is not empty.
func (e *encoder) optStr(name, s string) {
	if s != "" {
		e.field(name)
		e.str(s)
	}
}

// unixNano writes the member name as a quoted nanosecond timestamp, unless t is zero or unrepresentable.
func (e *encoder) unixNano(name string, nano int64) {
	if nano > 0 {
		e.field(name)
		e.int64Str(nano)
	}
}

func (e *encoder) resource(res *resource.Resource) {
	if res == nil || res.Len() == 0 {
		return
	}
	e.field("resource")
	e.beginObject()
	e.field("attributes")
	iter := res.Iter()
	e.attributeIter(&iter)
	e.endObject()
}

func (e *encoder) scope(s instrumentation.Scope) {
	if s.Name == "" && s.Version == "" && s.Attributes.Len() == 0 {
		return
	}
	e.field("scope")
	e.beginObject()
	e.optStr("name", s.Name)
	e.optStr("version", s.Version)
	if s.Attributes.Len() > 0 {
		e.field("attributes")
		iter := s.Attributes.Iter()
		e.attributeIter(&iter)
	}
	e.endObject()
}

// attributeSet writes the "attributes" member of a data point or exemplar, unless the set is empty.
func (e *encoder) attributeSet(name string, s attribute.Set) {
	if s.Len() == 0 {
		return
	}
	e.field(name)
	iter := s.Iter()
	e.attributeIter(&iter)
}

func (e *encoder) attributeSlice(name string, kvs []attribute.KeyValue) {
	if len(kvs) == 0 {
		return
	}
	e.field(name)
	e.beginArray()
	for _, kv := range kvs {
		e.elem()
		e.keyValue(kv)
	}
	e.endArray()
}

func (e *encoder) attributeIter(iter *attribute.Iterator) {
	e.beginArray()
	for iter.Next() {
		e.elem()
		e.keyValue(iter.Attribute())
	}
	e.endArray()
}

func (e *encoder) keyValue(kv attribute.KeyValue) {
	e.beginObject()
	e.field("key")
	e.str(string(kv.Key))
	e.field("value")
	e.anyValue(kv.Value)
	e.endObject()
}

// anyValue writes v as an OTLP AnyValue. Types are encoded the same way the
// upstream protobuf transform does.
func (e *encoder) anyValue(v attribute.Value) {
	e.beginObject()
	switch v.Type() {
	case attribute.STRING:
		e.field("stringValue")
		e.str(v.AsString())
	case attribute.BOOL:
		e.field("boolValue")
		e.boolean(v.AsBool())
	case attribute.INT64:
		e.field("intValue")
		e.int64Str(v.AsInt64())
	case attribute.FLOAT64:
		e.field("doubleValue")
		e.float(v.AsFloat64())
	case attribute.BYTESLICE:
		e.field("bytesValue")
		e.bytes(v.AsByteSlice())
	case attribute.STRINGSLICE:
		vals := v.AsStringSlice()
		e.array(len(vals), func(i int) {
			e.beginObject()
			e.field("stringValue")
			e.str(vals[i])
			e.endObject()
		})
	case attribute.INT64SLICE:
		vals := v.AsInt64Slice()
		e.array(len(vals), func(i int) {
			e.beginObject()
			e.field("intValue")
			e.int64Str(vals[i])
			e.endObject()
		})
	case attribute.FLOAT64SLICE:
		vals := v.AsFloat64Slice()
		e.array(len(vals), func(i int) {
			e.beginObject()
			e.field("doubleValue")
			e.float(vals[i])
			e.endObject()
		})
	case attribute.BOOLSLICE:
		vals := v.AsBoolSlice()
		e.array(len(vals), func(i int) {
			e.beginObject()
			e.field("boolValue")
			e.boolean(vals[i])
			e.endObject()
		})
	case attribute.SLICE:
		vals := v.AsSlice()
		e.array(len(vals), func(i int) { e.anyValue(vals[i]) })
	case attribute.MAP:
		e.field("kvlistValue")
		e.beginObject()
		e.attributeSlice("values", v.AsMap())
		e.endObject()
	case attribute.EMPTY:
		// An empty AnyValue is "{}".
	default:
		e.field("stringValue")
		e.str("INVALID")
	}
	e.endObject()
}

// array writes {"arrayValue":{"values":[...]}} members inside the current AnyValue object.
func (e *encoder) array(n int, elem func(i int)) {
	e.field("arrayValue")
	e.beginObject()
	e.field("values")
	e.beginArray()
	for i := range n {
		e.elem()
		elem(i)
	}
	e.endArray()
	e.endObject()
}
