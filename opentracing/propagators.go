package opentracing

import (
	"strconv"
	"strings"

	ot "github.com/opentracing/opentracing-go"
)

type textMapPropagator struct{}

const (
	prefixBaggage     = "ot-baggage-"
	prefixTracerState = "x-datadog-"

	fieldNameTraceID  = prefixTracerState + "trace-id"
	fieldNameParentID = prefixTracerState + "parent-id"
)

// Inject defines the textMapPropagator to propagate SpanContext data
// out of the current process. The implementation propagates the
// TraceID and the current active SpanID, as well as the Span baggage.
func (p *textMapPropagator) Inject(context ot.SpanContext, carrier interface{}) error {
	ctx, ok := context.(SpanContext)
	if !ok {
		return ot.ErrInvalidSpanContext
	}
	writer, ok := carrier.(ot.TextMapWriter)
	if !ok {
		return ot.ErrInvalidCarrier
	}

	// propagate the TraceID and the current active SpanID
	writer.Set(fieldNameTraceID, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(fieldNameParentID, strconv.FormatUint(ctx.spanID, 10))

	// propagate OpenTracing baggage
	for k, v := range ctx.baggage {
		writer.Set(prefixBaggage+k, v)
	}
	return nil
}

// Extract does
func (p *textMapPropagator) Extract(carrier interface{}) (ot.SpanContext, error) {
	reader, ok := carrier.(ot.TextMapReader)
	if !ok {
		return nil, ot.ErrInvalidCarrier
	}
	var err error
	var traceID, parentID uint64
	decodedBaggage := make(map[string]string)

	// extract SpanContext fields
	err = reader.ForeachKey(func(k, v string) error {
		switch strings.ToLower(k) {
		case fieldNameTraceID:
			traceID, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				return ot.ErrSpanContextCorrupted
			}
		case fieldNameParentID:
			parentID, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				return ot.ErrSpanContextCorrupted
			}
		default:
			lowercaseK := strings.ToLower(k)
			if strings.HasPrefix(lowercaseK, prefixBaggage) {
				decodedBaggage[strings.TrimPrefix(lowercaseK, prefixBaggage)] = v
			}
		}

		return nil
	})

	if traceID == 0 && parentID == 0 && len(decodedBaggage) == 0 {
		return nil, ot.ErrSpanContextNotFound
	}

	if err != nil {
		return nil, err
	}

	if traceID == 0 || parentID == 0 {
		return nil, ot.ErrSpanContextNotFound
	}

	return SpanContext{
		traceID: traceID,
		spanID:  parentID,
		baggage: decodedBaggage,
	}, nil
}
