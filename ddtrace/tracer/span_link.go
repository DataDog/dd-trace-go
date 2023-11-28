package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

type SpanLink struct {
	ddtrace.SpanLink
}

func NewSpanLink(ctx ddtrace.SpanContext, attr map[string]interface{}) ddtrace.SpanLink {
	var s ddtrace.SpanLink
	if p, ok := ctx.(ddtrace.SpanContextW3C); ok {
		tid := traceID(p.TraceID128Bytes())
		s.TraceID = tid.Lower()
		s.TraceIDHigh = tid.Upper()
	} else {
		s.TraceID = ctx.TraceID()
	}
	s.SpanID = ctx.SpanID()
	// TODO(dianashevchenko) : is it better to expand array attributes here in stead of in the span start
	s.Attributes = attr
	return s
}
