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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// DecodeMsg implements msgp.Decodable
func (z *span) DecodeMsg(dc *msgp.Reader) (err error) {
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
			z.Name, err = dc.ReadString()
			if err != nil {
				return
			}
		case "service":
			z.Service, err = dc.ReadString()
			if err != nil {
				return
			}
		case "resource":
			z.Resource, err = dc.ReadString()
			if err != nil {
				return
			}
		case "type":
			z.Type, err = dc.ReadString()
			if err != nil {
				return
			}
		case "start":
			z.Start, err = dc.ReadInt64()
			if err != nil {
				return
			}
		case "duration":
			z.Duration, err = dc.ReadInt64()
			if err != nil {
				return
			}
		case "meta":
			var zb0002 uint32
			zb0002, err = dc.ReadMapHeader()
			if err != nil {
				return
			}
			if z.Meta == nil {
				z.Meta = make(map[string]string, zb0002)
			} else if len(z.Meta) > 0 {
				for key := range z.Meta {
					delete(z.Meta, key)
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
				z.Meta[za0001] = za0002
			}
		case "span_links":
			var zb0003 uint32
			zb0003, err = dc.ReadArrayHeader()
			if err != nil {
				return
			}
			if cap(z.Links) >= int(zb0003) {
				z.Links = (z.Links)[:zb0003]
			} else {
				z.Links = make([]ddtrace.SpanLink, zb0003)
			}
			for za0003 := range z.Links {
				err = z.Links[za0003].DecodeMsg(dc)
				if err != nil {
					return
				}
			}
		case "metrics":
			var zb0004 uint32
			zb0004, err = dc.ReadMapHeader()
			if err != nil {
				return
			}
			if z.Metrics == nil {
				z.Metrics = make(map[string]float64, zb0004)
			} else if len(z.Metrics) > 0 {
				for key := range z.Metrics {
					delete(z.Metrics, key)
				}
			}
			for zb0004 > 0 {
				zb0004--
				var za0004 string
				var za0005 float64
				za0004, err = dc.ReadString()
				if err != nil {
					return
				}
				za0005, err = dc.ReadFloat64()
				if err != nil {
					return
				}
				z.Metrics[za0004] = za0005
			}
		case "span_id":
			z.SpanID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "trace_id":
			z.TraceID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "parent_id":
			z.ParentID, err = dc.ReadUint64()
			if err != nil {
				return
			}
		case "error":
			z.Error, err = dc.ReadInt32()
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
func (z *span) EncodeMsg(en *msgp.Writer) (err error) {
	// omitempty: check for empty values
	zb0001Len := uint32(13)
	var zb0001Mask uint16 /* 13 bits */
	if z.Meta == nil {
		zb0001Len--
		zb0001Mask |= 0x40
	}
	if z.Links == nil {
		zb0001Len--
		zb0001Mask |= 0x80
	}
	if z.Metrics == nil {
		zb0001Len--
		zb0001Mask |= 0x100
	}
	// variable map header, size zb0001Len
	err = en.Append(0x80 | uint8(zb0001Len))
	if err != nil {
		return
	}
	if zb0001Len == 0 {
		return
	}
	// write "name"
	err = en.Append(0xa4, 0x6e, 0x61, 0x6d, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.Name)
	if err != nil {
		return
	}
	// write "service"
	err = en.Append(0xa7, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.Service)
	if err != nil {
		return
	}
	// write "resource"
	err = en.Append(0xa8, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.Resource)
	if err != nil {
		return
	}
	// write "type"
	err = en.Append(0xa4, 0x74, 0x79, 0x70, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.Type)
	if err != nil {
		return
	}
	// write "start"
	err = en.Append(0xa5, 0x73, 0x74, 0x61, 0x72, 0x74)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.Start)
	if err != nil {
		return
	}
	// write "duration"
	err = en.Append(0xa8, 0x64, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.Duration)
	if err != nil {
		return
	}
	if (zb0001Mask & 0x40) == 0 { // if not empty
		// write "meta"
		err = en.Append(0xa4, 0x6d, 0x65, 0x74, 0x61)
		if err != nil {
			return
		}
		err = en.WriteMapHeader(uint32(len(z.Meta)))
		if err != nil {
			return
		}
		for za0001, za0002 := range z.Meta {
			err = en.WriteString(za0001)
			if err != nil {
				return
			}
			err = en.WriteString(za0002)
			if err != nil {
				return
			}
		}
	}
	if (zb0001Mask & 0x80) == 0 { // if not empty
		// write "span_links"
		err = en.Append(0xaa, 0x73, 0x70, 0x61, 0x6e, 0x5f, 0x6c, 0x69, 0x6e, 0x6b, 0x73)
		if err != nil {
			return
		}
		err = en.WriteArrayHeader(uint32(len(z.Links)))
		if err != nil {
			return
		}
		for za0003 := range z.Links {
			err = z.Links[za0003].EncodeMsg(en)
			if err != nil {
				return
			}
		}
	}
	if (zb0001Mask & 0x100) == 0 { // if not empty
		// write "metrics"
		err = en.Append(0xa7, 0x6d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x73)
		if err != nil {
			return
		}
		err = en.WriteMapHeader(uint32(len(z.Metrics)))
		if err != nil {
			return
		}
		for za0004, za0005 := range z.Metrics {
			err = en.WriteString(za0004)
			if err != nil {
				return
			}
			err = en.WriteFloat64(za0005)
			if err != nil {
				return
			}
		}
	}
	// write "span_id"
	err = en.Append(0xa7, 0x73, 0x70, 0x61, 0x6e, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.SpanID)
	if err != nil {
		return
	}
	// write "trace_id"
	err = en.Append(0xa8, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.TraceID)
	if err != nil {
		return
	}
	// write "parent_id"
	err = en.Append(0xa9, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.ParentID)
	if err != nil {
		return
	}
	// write "error"
	err = en.Append(0xa5, 0x65, 0x72, 0x72, 0x6f, 0x72)
	if err != nil {
		return
	}
	err = en.WriteInt32(z.Error)
	if err != nil {
		return
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z *span) Msgsize() (s int) {
	s = 1 + 5 + msgp.StringPrefixSize + len(z.Name) + 8 + msgp.StringPrefixSize + len(z.Service) + 9 + msgp.StringPrefixSize + len(z.Resource) + 5 + msgp.StringPrefixSize + len(z.Type) + 6 + msgp.Int64Size + 9 + msgp.Int64Size + 5 + msgp.MapHeaderSize
	if z.Meta != nil {
		for za0001, za0002 := range z.Meta {
			_ = za0002
			s += msgp.StringPrefixSize + len(za0001) + msgp.StringPrefixSize + len(za0002)
		}
	}
	s += 11 + msgp.ArrayHeaderSize
	for za0003 := range z.Links {
		s += z.Links[za0003].Msgsize()
	}
	s += 8 + msgp.MapHeaderSize
	if z.Metrics != nil {
		for za0004, za0005 := range z.Metrics {
			_ = za0005
			s += msgp.StringPrefixSize + len(za0004) + msgp.Float64Size
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
				(*z)[zb0001] = new(span)
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
					(*z)[zb0001][zb0002] = new(span)
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
