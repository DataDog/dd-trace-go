package tracer

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// HTTPHeadersCarrier wraps an http.Header as a TextMapWriter and TextMapReader, allowing
// it to be used using the provided Propagator implementation.
type HTTPHeadersCarrier http.Header

var _ TextMapWriter = (*HTTPHeadersCarrier)(nil)
var _ TextMapReader = (*HTTPHeadersCarrier)(nil)

// Set implements TextMapWriter.
func (c HTTPHeadersCarrier) Set(key, val string) {
	http.Header(c).Set(key, val)
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

// TextMapCarrier allows the use of a regular map[string]string as both TextMapWriter
// and TextMapReader, making it compatible with the provided Propagator.
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
	// DefaultBaggageHeaderPrefix specifies the prefix that will be used in
	// HTTP headers or text maps to prefix baggage keys.
	DefaultBaggageHeaderPrefix = "ot-baggage-"

	// DefaultTraceIDHeader specifies the key that will be used in HTTP headers
	// or text maps to store the trace ID.
	DefaultTraceIDHeader = "x-datadog-trace-id"

	// DefaultParentIDHeader specifies the key that will be used in HTTP headers
	// or text maps to store the parent ID.
	DefaultParentIDHeader = "x-datadog-parent-id"

	// DefaultPriorityHeader specifies the key that will be used in HTTP headers
	// or text maps to store the sampling priority value.
	DefaultPriorityHeader = "x-datadog-sampling-priority"
)

// originHeader specifies the name of the header indicating the origin of the trace.
// It is used with the Synthetics product and usually has the value "synthetics".
const originHeader = "x-datadog-origin"

// PropagatorConfig defines the configuration for initializing a propagator.
type PropagatorConfig struct {
	// BaggagePrefix specifies the prefix that will be used to store baggage
	// items in a map. It defaults to DefaultBaggageHeaderPrefix.
	BaggagePrefix string

	// TraceHeader specifies the map key that will be used to store the trace ID.
	// It defaults to DefaultTraceIDHeader.
	TraceHeader string

	// ParentHeader specifies the map key that will be used to store the parent ID.
	// It defaults to DefaultParentIDHeader.
	ParentHeader string

	// PriorityHeader specifies the map key that will be used to store the sampling priority.
	// It deafults to DefaultPriorityHeader.
	PriorityHeader string
}

// NewPropagator returns a new propagator which uses TextMap to inject
// and extract values. It propagates trace and span IDs and baggage.
// To use the defaults, nil may be provided in place of the config.
func NewPropagator(cfg *PropagatorConfig) Propagator {
	if cfg == nil {
		cfg = new(PropagatorConfig)
	}
	if cfg.BaggagePrefix == "" {
		cfg.BaggagePrefix = DefaultBaggageHeaderPrefix
	}
	if cfg.TraceHeader == "" {
		cfg.TraceHeader = DefaultTraceIDHeader
	}
	if cfg.ParentHeader == "" {
		cfg.ParentHeader = DefaultParentIDHeader
	}
	if cfg.PriorityHeader == "" {
		cfg.PriorityHeader = DefaultPriorityHeader
	}
	return &propagator{
		injectors:  makeInjectors(cfg),
		extractors: makeExtractors(cfg),
	}
}

// propagator implements a Propagator that supports TextMap carriers.
// It propagates the trace and span IDs, as well as the baggage from the
// context.
type propagator struct {
	injectors  []textMapPropagator
	extractors []textMapPropagator
}

// makeInjectors returns a list of injectors to apply when propagating a
// context. By default, only the datadog propagation style will be used.
// If the DD_PROPAGATION_STYLE_INJECT environment variable is set,
// this can override the default behavior.
func makeInjectors(cfg *PropagatorConfig) []textMapPropagator {
	dd := &datadogPropagator{cfg}
	b3 := &b3Propagator{}

	ps := os.Getenv("DD_PROPAGATION_STYLE_INJECT")
	if ps == "" {
		return []textMapPropagator{dd}
	}
	styles := propagationStyleList(ps)

	var injectors []textMapPropagator
	for _, v := range styles {
		switch v {
		case "Datadog":
			injectors = append(injectors, dd)
		case "B3":
			injectors = append(injectors, b3)
		default:
			// TODO: consider logging something for invalid/unknown styles.
		}
	}

	// If all the styles were invalid/unknown, then revert to default behavior.
	if len(injectors) == 0 {
		return []textMapPropagator{dd}
	}
	return injectors
}

func makeExtractors(cfg *PropagatorConfig) []textMapPropagator {
	dd := &datadogPropagator{cfg}
	b3 := &b3Propagator{}

	ps := os.Getenv("DD_PROPAGATION_STYLE_EXTRACT")
	if ps == "" {
		return []textMapPropagator{dd}
	}
	styles := propagationStyleList(ps)

	var extractors []textMapPropagator
	for _, v := range styles {
		switch v {
		case "Datadog":
			extractors = append(extractors, dd)
		case "B3":
			extractors = append(extractors, b3)
		default:
			// TODO: consider logging something for invalid/unknown styles.
		}
	}

	// If all the styles were invalid/unknown, then revert to default behavior.
	if len(extractors) == 0 {
		return []textMapPropagator{dd}
	}
	return extractors
}

// propagationStyleList splits a string containing propagation styles
// separated by space or comma, and returns a []string containing
// only the propagation styles.
func propagationStyleList(s string) []string {
	f := func(r rune) bool {
		switch r {
		case ' ', ',':
			return true
		}
		return false
	}
	return strings.FieldsFunc(s, f)
}

// Inject defines the Propagator to propagate SpanContext data
// out of the current process. The implementation propagates the
// TraceID and the current active SpanID, as well as the Span baggage.
func (p *propagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case TextMapWriter:
		// Apply all of the injectors.
		for _, v := range p.injectors {
			err := v.injectTextMap(spanCtx, c)
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrInvalidCarrier
	}
}

// Extract implements Propagator.
func (p *propagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch c := carrier.(type) {
	case TextMapReader:
		// Try each extractor. The first to successfully produce a context
		// will get returned.
		for _, v := range p.extractors {
			ctx, err := v.extractTextMap(c)
			if ctx != nil {
				return ctx, nil
			}
			// Special treatment for "context not found"
			if err == ErrSpanContextNotFound {
				continue
			}
			return nil, err
		}
		return nil, ErrSpanContextNotFound
	default:
		return nil, ErrInvalidCarrier
	}
}

// textMapPropagator is used to inject and extract span contexts.
type textMapPropagator interface {
	injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error
	extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error)
}

// datadogPropagator implements textMapPropagator and injects/extracts span contexts
// using datadog headers.
type datadogPropagator struct {
	cfg *PropagatorConfig
}

func (d *datadogPropagator) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}
	// propagate the TraceID and the current active SpanID
	writer.Set(d.cfg.TraceHeader, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(d.cfg.ParentHeader, strconv.FormatUint(ctx.spanID, 10))
	if ctx.hasSamplingPriority() {
		writer.Set(d.cfg.PriorityHeader, strconv.Itoa(ctx.samplingPriority()))
	}
	if ctx.origin != "" {
		writer.Set(originHeader, ctx.origin)
	}
	// propagate OpenTracing baggage
	for k, v := range ctx.baggage {
		writer.Set(d.cfg.BaggagePrefix+k, v)
	}
	return nil
}

func (d *datadogPropagator) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var ctx spanContext
	err := reader.ForeachKey(func(k, v string) error {
		var err error
		key := strings.ToLower(k)
		switch key {
		case d.cfg.TraceHeader:
			ctx.traceID, err = parseUint64(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case d.cfg.ParentHeader:
			ctx.spanID, err = parseUint64(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case d.cfg.PriorityHeader:
			priority, err := strconv.Atoi(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
			ctx.setSamplingPriority(priority)
		case originHeader:
			ctx.origin = v
		default:
			if strings.HasPrefix(key, d.cfg.BaggagePrefix) {
				ctx.setBaggageItem(strings.TrimPrefix(key, d.cfg.BaggagePrefix), v)
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

type b3Propagator struct{}

func (*b3Propagator) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}
	// propagate the TraceID and the current active SpanID
	writer.Set("x-b3-traceid", strconv.FormatUint(ctx.traceID, 16))
	writer.Set("x-b3-spanid", strconv.FormatUint(ctx.spanID, 16))
	if ctx.hasSamplingPriority() {
		writer.Set("x-b3-sampled", strconv.Itoa(ctx.samplingPriority()))
	}
	return nil
}

func (*b3Propagator) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var ctx spanContext
	err := reader.ForeachKey(func(k, v string) error {
		var err error
		key := strings.ToLower(k)
		switch key {
		case "x-b3-traceid":
			ctx.traceID, err = strconv.ParseUint(v, 16, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case "x-b3-parentspanid":
			ctx.spanID, err = strconv.ParseUint(v, 16, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case "x-b3-sampled":
			priority, err := strconv.Atoi(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
			ctx.setSamplingPriority(priority)
		default:
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
