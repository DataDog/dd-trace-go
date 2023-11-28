// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package ddtrace contains the interfaces that specify the implementations of Datadog's
// tracing library, as well as a set of sub-packages containing various implementations:
// our native implementation ("tracer"), a wrapper that can be used with Opentracing
// ("opentracer") and a mock tracer to be used for testing ("mocktracer"). Additionally,
// package "ext" provides a set of tag names and values specific to Datadog's APM product.
//
// To get started, visit the documentation for any of the packages you'd like to begin
// with by accessing the subdirectories of this package: https://godoc.org/gopkg.in/DataDog/dd-trace-go.v1/ddtrace#pkg-subdirectories.
package ddtrace // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace"

import (
	"context"
	"github.com/tinylib/msgp/msgp"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// SpanContextW3C represents a SpanContext with an additional method to allow
// access of the 128-bit trace id of the span, if present.
type SpanContextW3C interface {
	SpanContext

	// TraceID128 returns the hex-encoded 128-bit trace ID that this context is carrying.
	// The string will be exactly 32 bytes and may include leading zeroes.
	TraceID128() string

	// TraceID128 returns the raw bytes of the 128-bit trace ID that this context is carrying.
	TraceID128Bytes() [16]byte
}

// Tracer specifies an implementation of the Datadog tracer which allows starting
// and propagating spans. The official implementation if exposed as functions
// within the "tracer" package.
type Tracer interface {
	// StartSpan starts a span with the given operation name and options.
	StartSpan(operationName string, opts ...StartSpanOption) Span

	// Extract extracts a span context from a given carrier. Note that baggage item
	// keys will always be lower-cased to maintain consistency. It is impossible to
	// maintain the original casing due to MIME header canonicalization standards.
	Extract(carrier interface{}) (SpanContext, error)

	// Inject injects a span context into the given carrier.
	Inject(context SpanContext, carrier interface{}) error

	// Stop stops the tracer. Calls to Stop should be idempotent.
	Stop()
}

// Span represents a chunk of computation time. Spans have names, durations,
// timestamps and other metadata. A Tracer is used to create hierarchies of
// spans in a request, buffer and submit them to the server.
type Span interface {
	// SetTag sets a key/value pair as metadata on the span.
	SetTag(key string, value interface{})

	// SetOperationName sets the operation name for this span. An operation name should be
	// a representative name for a group of spans (e.g. "grpc.server" or "http.request").
	SetOperationName(operationName string)

	// BaggageItem returns the baggage item held by the given key.
	BaggageItem(key string) string

	// SetBaggageItem sets a new baggage item at the given key. The baggage
	// item should propagate to all descendant spans, both in- and cross-process.
	SetBaggageItem(key, val string)

	// AddLinks sets the given set of links on the span.
	AddLinks(links ...SpanLink)

	// Finish finishes the current span with the given options. Finish calls should be idempotent.
	Finish(opts ...FinishOption)

	// Context returns the SpanContext of this Span.
	Context() SpanContext
}

// SpanContext represents a span state that can propagate to descendant spans
// and across process boundaries. It contains all the information needed to
// spawn a direct descendant of the span that it belongs to. It can be used
// to create distributed tracing by propagating it using the provided interfaces.
type SpanContext interface {
	// SpanID returns the span ID that this context is carrying.
	SpanID() uint64

	// TraceID returns the trace ID that this context is carrying.
	TraceID() uint64

	// ForeachBaggageItem provides an iterator over the key/value pairs set as
	// baggage within this context. Iteration stops when the handler returns
	// false.
	ForeachBaggageItem(handler func(k, v string) bool)
}

// StartSpanOption is a configuration option that can be used with a Tracer's StartSpan method.
type StartSpanOption func(cfg *StartSpanConfig)

// FinishOption is a configuration option that can be used with a Span's Finish method.
type FinishOption func(cfg *FinishConfig)

// FinishConfig holds the configuration for finishing a span. It is usually passed around by
// reference to one or more FinishOption functions which shape it into its final form.
type FinishConfig struct {
	// FinishTime represents the time that should be set as finishing time for the
	// span. Implementations should use the current time when FinishTime.IsZero().
	FinishTime time.Time

	// Error holds an optional error that should be set on the span before
	// finishing.
	Error error

	// NoDebugStack will prevent any set errors from generating an attached stack trace tag.
	NoDebugStack bool

	// StackFrames specifies the number of stack frames to be attached in spans that finish with errors.
	StackFrames uint

	// SkipStackFrames specifies the offset at which to start reporting stack frames from the stack.
	SkipStackFrames uint
}

// StartSpanConfig holds the configuration for starting a new span. It is usually passed
// around by reference to one or more StartSpanOption functions which shape it into its
// final form.
type StartSpanConfig struct {
	// Parent holds the SpanContext that should be used as a parent for the
	// new span. If nil, implementations should return a root span.
	Parent SpanContext

	// StartTime holds the time that should be used as the start time of the span.
	// Implementations should use the current time when StartTime.IsZero().
	StartTime time.Time

	// Tags holds a set of key/value pairs that should be set as metadata on the
	// new span.
	Tags map[string]interface{}

	// Tags holds a set of key/value pairs that should be set as metadata on the
	// new span.
	Links []SpanLink

	// SpanID will be the SpanID of the Span, overriding the random number that would
	// be generated. If no Parent SpanContext is present, then this will also set the
	// TraceID to the same value.
	SpanID uint64

	// Context is the parent context where the span should be stored.
	Context context.Context
}

// Logger implementations are able to log given messages that the tracer or profiler might output.
type Logger interface {
	// Log prints the given message.
	Log(msg string)
}

// UseLogger sets l as the logger for all tracer and profiler logs.
func UseLogger(l Logger) {
	log.UseLogger(l)
}

type SpanLink struct {
	TraceID     uint64 `msg:"trace_id"`
	TraceIDHigh uint64 `msg:"trace_id_high,omitempty"`
	SpanID      uint64 `msg:"span_id"`

	Attributes map[string]interface{} `msg:"attributes,omitempty"`
	Tracestate string                 `msg:"tracestate,omitempty"`
	Flags      uint32                 `msg:"flags,omitempty"`

	//TODO (dianashevchenko): do we need to expose dropped attributes count
	DroppedAttributes int32 `msg:"dropped_attributes_count,omitempty"`
}

// DecodeMsg implements msgp.Decodable
func (z *SpanLink) DecodeMsg(dc *msgp.Reader) (err error) {
	var field []byte
	_ = field
	var zb0001 uint32
	zb0001, err = dc.ReadMapHeader()
	if err != nil {
		err = msgp.WrapError(err)
		return
	}
	for zb0001 > 0 {
		zb0001--
		field, err = dc.ReadMapKeyPtr()
		if err != nil {
			err = msgp.WrapError(err)
			return
		}
		switch msgp.UnsafeString(field) {
		case "trace_id":
			z.TraceID, err = dc.ReadUint64()
			if err != nil {
				err = msgp.WrapError(err, "TraceID")
				return
			}
		case "trace_id_high":
			z.TraceIDHigh, err = dc.ReadUint64()
			if err != nil {
				err = msgp.WrapError(err, "TraceIDHigh")
				return
			}
		case "span_id":
			z.SpanID, err = dc.ReadUint64()
			if err != nil {
				err = msgp.WrapError(err, "SpanID")
				return
			}
		case "attributes":
			var zb0002 uint32
			zb0002, err = dc.ReadMapHeader()
			if err != nil {
				err = msgp.WrapError(err, "Attributes")
				return
			}
			if z.Attributes == nil {
				z.Attributes = make(map[string]interface{}, zb0002)
			} else if len(z.Attributes) > 0 {
				for key := range z.Attributes {
					delete(z.Attributes, key)
				}
			}
			for zb0002 > 0 {
				zb0002--
				var za0001 string
				var za0002 interface{}
				za0001, err = dc.ReadString()
				if err != nil {
					err = msgp.WrapError(err, "Attributes")
					return
				}
				za0002, err = dc.ReadIntf()
				if err != nil {
					err = msgp.WrapError(err, "Attributes", za0001)
					return
				}
				z.Attributes[za0001] = za0002
			}
		case "tracestate":
			z.Tracestate, err = dc.ReadString()
			if err != nil {
				err = msgp.WrapError(err, "Tracestate")
				return
			}
		case "flags":
			z.Flags, err = dc.ReadUint32()
			if err != nil {
				err = msgp.WrapError(err, "Flags")
				return
			}
		case "dropped_attributes_count":
			z.DroppedAttributes, err = dc.ReadInt32()
			if err != nil {
				err = msgp.WrapError(err, "droppedAttributes")
				return
			}
		default:
			err = dc.Skip()
			if err != nil {
				err = msgp.WrapError(err)
				return
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z *SpanLink) EncodeMsg(en *msgp.Writer) (err error) {
	// omitempty: check for empty values
	zb0001Len := uint32(7)
	var zb0001Mask uint8 /* 7 bits */
	if z.Attributes == nil {
		zb0001Len--
		zb0001Mask |= 0x8
	}
	if z.Tracestate == "" {
		zb0001Len--
		zb0001Mask |= 0x10
	}
	if z.Flags == 0 {
		zb0001Len--
		zb0001Mask |= 0x20
	}
	if z.DroppedAttributes == 0 {
		zb0001Len--
		zb0001Mask |= 0x40
	}
	// variable map header, size zb0001Len
	err = en.Append(0x80 | uint8(zb0001Len))
	if err != nil {
		return
	}
	if zb0001Len == 0 {
		return
	}
	// write "trace_id"
	err = en.Append(0xa8, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.TraceID)
	if err != nil {
		err = msgp.WrapError(err, "TraceID")
		return
	}
	// write "trace_id_high"
	err = en.Append(0xad, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x5f, 0x68, 0x69, 0x67, 0x68)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.TraceIDHigh)
	if err != nil {
		err = msgp.WrapError(err, "TraceIDHigh")
		return
	}
	// write "span_id"
	err = en.Append(0xa7, 0x73, 0x70, 0x61, 0x6e, 0x5f, 0x69, 0x64)
	if err != nil {
		return
	}
	err = en.WriteUint64(z.SpanID)
	if err != nil {
		err = msgp.WrapError(err, "SpanID")
		return
	}
	if (zb0001Mask & 0x8) == 0 { // if not empty
		// write "attributes"
		err = en.Append(0xaa, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65, 0x73)
		if err != nil {
			return
		}
		err = en.WriteMapHeader(uint32(len(z.Attributes)))
		if err != nil {
			err = msgp.WrapError(err, "Attributes")
			return
		}
		for za0001, za0002 := range z.Attributes {
			err = en.WriteString(za0001)
			if err != nil {
				err = msgp.WrapError(err, "Attributes")
				return
			}
			err = en.WriteIntf(za0002)
			if err != nil {
				err = msgp.WrapError(err, "Attributes", za0001)
				return
			}
		}
	}
	if (zb0001Mask & 0x10) == 0 { // if not empty
		// write "tracestate"
		err = en.Append(0xaa, 0x74, 0x72, 0x61, 0x63, 0x65, 0x73, 0x74, 0x61, 0x74, 0x65)
		if err != nil {
			return
		}
		err = en.WriteString(z.Tracestate)
		if err != nil {
			err = msgp.WrapError(err, "Tracestate")
			return
		}
	}
	if (zb0001Mask & 0x20) == 0 { // if not empty
		// write "flags"
		err = en.Append(0xa5, 0x66, 0x6c, 0x61, 0x67, 0x73)
		if err != nil {
			return
		}
		err = en.WriteUint32(z.Flags)
		if err != nil {
			err = msgp.WrapError(err, "Flags")
			return
		}
	}
	if (zb0001Mask & 0x40) == 0 { // if not empty
		// write "dropped_attributes_count"
		err = en.Append(0xb8, 0x64, 0x72, 0x6f, 0x70, 0x70, 0x65, 0x64, 0x5f, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65, 0x73, 0x5f, 0x63, 0x6f, 0x75, 0x6e, 0x74)
		if err != nil {
			return
		}
		err = en.WriteInt32(z.DroppedAttributes)
		if err != nil {
			err = msgp.WrapError(err, "droppedAttributes")
			return
		}
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z *SpanLink) Msgsize() (s int) {
	s = 1 + 9 + msgp.Uint64Size + 14 + msgp.Uint64Size + 8 + msgp.Uint64Size + 11 + msgp.MapHeaderSize
	if z.Attributes != nil {
		for za0001, za0002 := range z.Attributes {
			_ = za0002
			s += msgp.StringPrefixSize + len(za0001) + msgp.GuessSize(za0002)
		}
	}
	s += 11 + msgp.StringPrefixSize + len(z.Tracestate) + 6 + msgp.Uint32Size + 25 + msgp.Int32Size
	return
}
