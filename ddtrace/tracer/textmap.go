package tracer

import (
	"net/http"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// HTTPHeadersCarrier wraps an http.Header as a TextMapWriter and TextMapReader.
type HTTPHeadersCarrier http.Header

var _ TextMapWriter = (*HTTPHeadersCarrier)(nil)
var _ TextMapReader = (*HTTPHeadersCarrier)(nil)

// Set implements TextMapWriter.
func (c HTTPHeadersCarrier) Set(key, val string) {
	h := http.Header(c)
	h.Add(key, val)
}

// ForeachKey implements TextMapReader.
func (c HTTPHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, vals := range c {
		for _, v := range vals {
			if err := handler(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// TextMapCarrier allows the use of a regular map[string]string
// as both TextMapWriter and TextMapReader.
type TextMapCarrier map[string]string

var _ TextMapWriter = (*TextMapCarrier)(nil)
var _ TextMapReader = (*TextMapCarrier)(nil)

// Set implements TextMapWriter.
func (c TextMapCarrier) Set(key, val string) {
	c[key] = val
}

// ForeachKey conforms to the TextMapReader interface.
func (c TextMapCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

const (
	defaultBaggageHeaderPrefix = "ot-baggage-"
	defaultTraceIDHeader       = "x-datadog-trace-id"
	defaultParentIDHeader      = "x-datadog-parent-id"
)

// NewPropagator returns a new propagator which uses TextMap to inject
// and extract values. It propagates trace and span IDs and baggage.
// The parameters specify the prefix that will be used to prefix baggage header
// keys along with the trace and parent header. Empty strings may be provided
// to use the defaults, which are: "ot-baggage-" as prefix for baggage headers,
// "x-datadog-trace-id" and "x-datadog-parent-id" for trace and parent ID headers.
func NewPropagator(baggagePrefix, traceHeader, parentHeader string) Propagator {
	if baggagePrefix == "" {
		baggagePrefix = defaultBaggageHeaderPrefix
	}
	if traceHeader == "" {
		traceHeader = defaultTraceIDHeader
	}
	if parentHeader == "" {
		parentHeader = defaultParentIDHeader
	}
	return &propagator{baggagePrefix, traceHeader, parentHeader}
}

// propagator implements a propagator which uses TextMap internally.
// It propagates the trace and span IDs, as well as the baggage from the
// context.
type propagator struct {
	baggagePrefix string
	traceHeader   string
	parentHeader  string
}

// Inject defines the Propagator to propagate SpanContext data
// out of the current process. The implementation propagates the
// TraceID and the current active SpanID, as well as the Span baggage.
func (p *propagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch v := carrier.(type) {
	case TextMapWriter:
		return p.injectTextMap(spanCtx, v)
	default:
		return ErrInvalidCarrier
	}
}

func (p *propagator) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}
	// propagate the TraceID and the current active SpanID
	writer.Set(p.traceHeader, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(p.parentHeader, strconv.FormatUint(ctx.spanID, 10))
	// propagate OpenTracing baggage
	for k, v := range ctx.baggage {
		writer.Set(p.baggagePrefix+k, v)
	}
	return nil
}

// Extract implements Propagator.
func (p *propagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch v := carrier.(type) {
	case TextMapReader:
		return p.extractTextMap(v)
	default:
		return nil, ErrInvalidCarrier
	}
}

func (p *propagator) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var ctx spanContext
	err := reader.ForeachKey(func(k, v string) error {
		var err error
		key := strings.ToLower(k)
		switch key {
		case p.traceHeader:
			ctx.traceID, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case p.parentHeader:
			ctx.spanID, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		default:
			if strings.HasPrefix(key, p.baggagePrefix) {
				ctx.setBaggageItem(strings.TrimPrefix(key, p.baggagePrefix), v)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ctx.traceID == 0 || ctx.spanID == 0 {
		return nil, ErrSpanContextNotFound
	}
	return &ctx, nil
}
