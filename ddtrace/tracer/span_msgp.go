// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

// NOTE: THIS FILE WAS PRODUCED BY THE
// MSGP CODE GENERATION TOOL (github.com/tinylib/msgp)
// DO NOT EDIT

import (
	"github.com/tinylib/msgp/msgp"
)

// DecodeMsg implements msgp.Decodable
func (z *Span) DecodeMsg(dc *msgp.Reader) (err error) {
	var field []byte
	_ = field
	var zb0001 uint32
	zb0001, err = dc.ReadMapHeader()
	if err != nil {
		return
	}
	for zb0001 > 0 {
		zb0001--
		field, err = dc.ReadMapKeyPtr()
		if err != nil {
			return
		}
		switch msgp.UnsafeString(field) {
		case "name":
			z.name, err = dc.ReadString()
			if err != nil {
				return
			}
		case "service":
			z.service, err = dc.ReadString()
			if err != nil {
				return
			}
		case "resource":
			z.resource, err = dc.ReadString()
			if err != nil {
				return
			}
		case "type":
			z.spanType, err = dc.ReadString()
			if err != nil {
				return
			}
		case "start":
			z.start, err = dc.ReadInt64()
			if err != nil {
				return
			}
		case "duration":
			z.duration, err = dc.ReadInt64()
			if err != nil {
				return
			}
		case "meta":
			var zb0002 uint32
			zb0002, err = dc.ReadMapHeader()
			if err != nil {
				return
			}
			if z.meta == nil && zb0002 > 0 {
				z.meta = make(map[string]string, zb0002)
			} else if len(z.meta) > 0 {
				for key := range z.meta {
					delete(z.meta, key)
				}
			}
			for zb0002 > 0 {
				zb0002--
				var za0001 string
				var za0002 string
				za0001, err = dc.ReadString()
				if err != nil {
					return
				}
				za0002, err = dc.ReadString()
				if err != nil {
					return
				}
				z.meta[za0001] = za0002
			}
		case "metrics":
			var zb0003 uint32
			zb0003, err = dc.ReadMapHeader()
			if err != nil {
				return
			}
			if z.metrics == nil && zb0003 > 0 {
				z.metrics = make(map[string]float64, zb0003)
			} else if len(z.metrics) > 0 {
				for key := range z.metrics {
					delete(z.metrics, key)
				}
			}
			for zb0003 > 0 {
				zb0003--
				var za0003 string
				var za0004 float64
				za0003, err = dc.ReadString()
				if err != nil {
					return
				}
				za0004, err = dc.ReadFloat64()
				if err != nil {
					return
				}
				z.metrics[za0003] = za0004
			}
		case "span_id":
			z.spanID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "trace_id":
			z.traceID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "parent_id":
			z.parentID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "error":
			z.error, err = dc.ReadInt32()
			if err != nil {
				return
			}
		default:
			err = dc.Skip()
			if err != nil {
				return
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z *Span) EncodeMsg(en *msgp.Writer) (err error) {
	// map header, size 12
	// write "name"
	err = en.Append(0x8c, 0xa4, 0x6e, 0x61, 0x6d, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.name)
	if err != nil {
		return
	}
	// write "service"
	err = en.Append(0xa7, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.service)
	if err != nil {
		return
	}
	// write "resource"
	err = en.Append(0xa8, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.resource)
	if err != nil {
		return
	}
	// write "type"
	err = en.Append(0xa4, 0x74, 0x79, 0x70, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.spanType)
	if err != nil {
		return
	}
	// write "start"
	err = en.Append(0xa5, 0x73, 0x74, 0x61, 0x72, 0x74)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.start)
	if err != nil {
		return
	}
	// write "duration"
	err = en.Append(0xa8, 0x64, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.duration)
	if err != nil {
		return
	}
	// write "meta"
	err = en.Append(0xa4, 0x6d, 0x65, 0x74, 0x61)
	if err != nil {
		return
	}
	err = en.WriteMapHeader(uint32(len(z.meta)))
	if err != nil {
		return
	}
	for za0001, za0002 := range z.meta {
		err = en.WriteString(za0001)
		if err != nil {
			return
		}
		err = en.WriteString(za0002)
		if err != nil {
			return
		}
	}
	// write "metrics"
	err = en.Append(0xa7, 0x6d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x73)
	if err != nil {
		return
	}
	err = en.WriteMapHeader(uint32(len(z.metrics)))
	if err != nil {
		return
	}
	for za0003, za0004 := range z.metrics {
		err = en.WriteString(za0003)
		if err != nil {
			return
		}
		err = en.WriteFloat64(za0004)
		if err != nil {
			return
		}
	}
	// write "span_id"
	err = en.Append(0xa7, 0x73, 0x70, 0x61, 0x6e, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.spanID)
	if err != nil {
		return
	}
	// write "trace_id"
	err = en.Append(0xa8, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.traceID)
	if err != nil {
		return
	}
	// write "parent_id"
	err = en.Append(0xa9, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.parentID)
	if err != nil {
		return
	}
	// write "error"
	err = en.Append(0xa5, 0x65, 0x72, 0x72, 0x6f, 0x72)
	if err != nil {
		return
	}
	err = en.WriteInt32(z.error)
	if err != nil {
		return
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z *Span) Msgsize() (s int) {
	s = 1 + 5 + msgp.StringPrefixSize + len(z.name) + 8 + msgp.StringPrefixSize + len(z.service) + 9 + msgp.StringPrefixSize + len(z.resource) + 5 + msgp.StringPrefixSize + len(z.spanType) + 6 + msgp.Int64Size + 9 + msgp.Int64Size + 5 + msgp.MapHeaderSize
	if z.meta != nil {
		for za0001, za0002 := range z.meta {
			_ = za0002
			s += msgp.StringPrefixSize + len(za0001) + msgp.StringPrefixSize + len(za0002)
		}
	}
	s += 8 + msgp.MapHeaderSize
	if z.metrics != nil {
		for za0003, za0004 := range z.metrics {
			_ = za0004
			s += msgp.StringPrefixSize + len(za0003) + msgp.Float64Size
		}
	}
	s += 8 + msgp.Uint64Size + 9 + msgp.Uint64Size + 10 + msgp.Uint64Size + 6 + msgp.Int32Size
	return
}

// DecodeMsg implements msgp.Decodable
func (z *spanList) DecodeMsg(dc *msgp.Reader) (err error) {
	var zb0002 uint32
	zb0002, err = dc.ReadArrayHeader()
	if err != nil {
		return
	}
	if cap((*z)) >= int(zb0002) {
		(*z) = (*z)[:zb0002]
	} else {
		(*z) = make(spanList, zb0002)
	}
	for zb0001 := range *z {
		if dc.IsNil() {
			err = dc.ReadNil()
			if err != nil {
				return
			}
			(*z)[zb0001] = nil
		} else {
			if (*z)[zb0001] == nil {
				(*z)[zb0001] = new(Span)
			}
			err = (*z)[zb0001].DecodeMsg(dc)
			if err != nil {
				return
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z spanList) EncodeMsg(en *msgp.Writer) (err error) {
	err = en.WriteArrayHeader(uint32(len(z)))
	if err != nil {
		return
	}
	for zb0003 := range z {
		if z[zb0003] == nil {
			err = en.WriteNil()
			if err != nil {
				return
			}
		} else {
			err = z[zb0003].EncodeMsg(en)
			if err != nil {
				return
			}
		}
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z spanList) Msgsize() (s int) {
	s = msgp.ArrayHeaderSize
	for zb0003 := range z {
		if z[zb0003] == nil {
			s += msgp.NilSize
		} else {
			s += z[zb0003].Msgsize()
		}
	}
	return
}

// DecodeMsg implements msgp.Decodable
func (z *spanLists) DecodeMsg(dc *msgp.Reader) (err error) {
	var zb0003 uint32
	zb0003, err = dc.ReadArrayHeader()
	if err != nil {
		return
	}
	if cap((*z)) >= int(zb0003) {
		(*z) = (*z)[:zb0003]
	} else {
		(*z) = make(spanLists, zb0003)
	}
	for zb0001 := range *z {
		var zb0004 uint32
		zb0004, err = dc.ReadArrayHeader()
		if err != nil {
			return
		}
		if cap((*z)[zb0001]) >= int(zb0004) {
			(*z)[zb0001] = ((*z)[zb0001])[:zb0004]
		} else {
			(*z)[zb0001] = make(spanList, zb0004)
		}
		for zb0002 := range (*z)[zb0001] {
			if dc.IsNil() {
				err = dc.ReadNil()
				if err != nil {
					return
				}
				(*z)[zb0001][zb0002] = nil
			} else {
				if (*z)[zb0001][zb0002] == nil {
					(*z)[zb0001][zb0002] = new(Span)
				}
				err = (*z)[zb0001][zb0002].DecodeMsg(dc)
				if err != nil {
					return
				}
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z spanLists) EncodeMsg(en *msgp.Writer) (err error) {
	err = en.WriteArrayHeader(uint32(len(z)))
	if err != nil {
		return
	}
	for zb0005 := range z {
		err = en.WriteArrayHeader(uint32(len(z[zb0005])))
		if err != nil {
			return
		}
		for zb0006 := range z[zb0005] {
			if z[zb0005][zb0006] == nil {
				err = en.WriteNil()
				if err != nil {
					return
				}
			} else {
				err = z[zb0005][zb0006].EncodeMsg(en)
				if err != nil {
					return
				}
			}
		}
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z spanLists) Msgsize() (s int) {
	s = msgp.ArrayHeaderSize
	for zb0005 := range z {
		s += msgp.ArrayHeaderSize
		for zb0006 := range z[zb0005] {
			if z[zb0005][zb0006] == nil {
				s += msgp.NilSize
			} else {
				s += z[zb0005][zb0006].Msgsize()
			}
		}
	}
	return
}
