// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
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
	headerPropagationStyleInject  = "DD_TRACE_PROPAGATION_STYLE_INJECT"
	headerPropagationStyleExtract = "DD_TRACE_PROPAGATION_STYLE_EXTRACT"
	headerPropagationStyle        = "DD_TRACE_PROPAGATION_STYLE"

	headerPropagationStyleInjectDeprecated  = "DD_PROPAGATION_STYLE_INJECT"  // deprecated
	headerPropagationStyleExtractDeprecated = "DD_PROPAGATION_STYLE_EXTRACT" // deprecated
)

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

// traceTagsHeader holds the propagated trace tags
const traceTagsHeader = "x-datadog-tags"

// propagationExtractMaxSize limits the total size of incoming propagated tags to parse
const propagationExtractMaxSize = 512

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
	// It defaults to DefaultPriorityHeader.
	PriorityHeader string

	// MaxTagsHeaderLen specifies the maximum length of trace tags header value.
	// It defaults to defaultMaxTagsHeaderLen, a value of 0 disables propagation of tags.
	MaxTagsHeaderLen int

	// B3 specifies if B3 headers should be added for trace propagation.
	// See https://github.com/openzipkin/b3-propagation
	B3 bool
}

// NewPropagator returns a new propagator which uses TextMap to inject
// and extract values. It propagates trace and span IDs and baggage.
// To use the defaults, nil may be provided in place of the config.
//
// The inject and extract propagators are determined using environment variables
// with the following order of precedence:
//  1. DD_TRACE_PROPAGATION_STYLE_INJECT
//  2. DD_PROPAGATION_STYLE_INJECT (deprecated)
//  3. DD_TRACE_PROPAGATION_STYLE (applies to both inject and extract)
//  4. If none of the above, use default values
func NewPropagator(cfg *PropagatorConfig, propagators ...Propagator) Propagator {
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
	if len(propagators) > 0 {
		return &chainedPropagator{
			injectors:  propagators,
			extractors: propagators,
		}
	}
	injectorsPs := os.Getenv(headerPropagationStyleInject)
	if injectorsPs == "" {
		if injectorsPs = os.Getenv(headerPropagationStyleInjectDeprecated); injectorsPs != "" {
			log.Warn("%v is deprecated. Please use %v or %v instead.\n", headerPropagationStyleInjectDeprecated, headerPropagationStyleInject, headerPropagationStyle)
		}
	}
	extractorsPs := os.Getenv(headerPropagationStyleExtract)
	if extractorsPs == "" {
		if extractorsPs = os.Getenv(headerPropagationStyleExtractDeprecated); extractorsPs != "" {
			log.Warn("%v is deprecated. Please use %v or %v instead.\n", headerPropagationStyleExtractDeprecated, headerPropagationStyleExtract, headerPropagationStyle)
		}
	}
	return &chainedPropagator{
		injectors:  getPropagators(cfg, injectorsPs),
		extractors: getPropagators(cfg, extractorsPs),
	}
}

// chainedPropagator implements Propagator and applies a list of injectors and extractors.
// When injecting, all injectors are called to propagate the span context.
// When extracting, it tries each extractor, selecting the first successful one.
type chainedPropagator struct {
	injectors  []Propagator
	extractors []Propagator
}

// getPropagators returns a list of propagators based on ps, which is a comma seperated
// list of propagators. If the list doesn't contain any valid values, the
// default propagator will be returned. Any invalid values in the list will log
// a warning and be ignored.
func getPropagators(cfg *PropagatorConfig, ps string) []Propagator {
	dd := &propagator{cfg}
	defaultPs := []Propagator{&propagatorW3c{}, dd}
	if cfg.B3 {
		defaultPs = append(defaultPs, &propagatorB3{})
	}
	if ps == "" {
		if prop := os.Getenv(headerPropagationStyle); prop != "" {
			ps = prop // use the generic DD_TRACE_PROPAGATION_STYLE if set
		} else {
			return defaultPs // no env set, so use default from configuration
		}
	}
	if ps == "none" {
		return nil
	}
	var list []Propagator
	if cfg.B3 {
		list = append(list, &propagatorB3{})
	}
	for _, v := range strings.Split(ps, ",") {
		switch strings.ToLower(v) {
		case "datadog":
			list = append(list, dd)
		case "tracecontext":
			list = append([]Propagator{&propagatorW3c{}}, list...)
		case "b3", "b3multi":
			if !cfg.B3 {
				// propagatorB3 hasn't already been added, add a new one.
				list = append(list, &propagatorB3{})
			}
		case "none":
			log.Warn("Propagator \"none\" has no effect when combined with other propagators. " +
				"To disable the propagator, set to `none`")
		default:
			log.Warn("unrecognized propagator: %s\n", v)
		}
	}
	if len(list) == 0 {
		return defaultPs // no valid propagators, so return default
	}
	return list
}

// Inject defines the Propagator to propagate SpanContext data
// out of the current process. The implementation propagates the
// TraceID and the current active SpanID, as well as the Span baggage.
func (p *chainedPropagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	for _, v := range p.injectors {
		err := v.Inject(spanCtx, carrier)
		if err != nil {
			return err
		}
	}
	return nil
}

// Extract implements Propagator.
func (p *chainedPropagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	for _, v := range p.extractors {
		ctx, err := v.Extract(carrier)
		if ctx != nil {
			// first extractor returns
			log.Debug("Extracted span context: %#v", ctx)
			return ctx, nil
		}
		if err == ErrSpanContextNotFound {
			continue
		}
		return nil, err
	}
	return nil, ErrSpanContextNotFound
}

// propagator implements Propagator and injects/extracts span contexts
// using datadog headers. Only TextMap carriers are supported.
type propagator struct {
	cfg *PropagatorConfig
}

func (p *propagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case TextMapWriter:
		return p.injectTextMap(spanCtx, c)
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
	writer.Set(p.cfg.TraceHeader, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(p.cfg.ParentHeader, strconv.FormatUint(ctx.spanID, 10))
	if sp, ok := ctx.samplingPriority(); ok {
		writer.Set(p.cfg.PriorityHeader, strconv.Itoa(sp))
	}
	if ctx.origin != "" {
		writer.Set(originHeader, ctx.origin)
	}
	// propagate OpenTracing baggage
	for k, v := range ctx.baggage {
		writer.Set(p.cfg.BaggagePrefix+k, v)
	}
	if p.cfg.MaxTagsHeaderLen <= 0 {
		return nil
	}
	if s := p.marshalPropagatingTags(ctx); len(s) > 0 {
		writer.Set(traceTagsHeader, s)
	}
	return nil
}

// marshalPropagatingTags marshals all propagating tags included in ctx to a comma separated string
func (p *propagator) marshalPropagatingTags(ctx *spanContext) string {
	var sb strings.Builder
	if ctx.trace == nil {
		return ""
	}
	ctx.trace.mu.Lock()
	defer ctx.trace.mu.Unlock()
	for k, v := range ctx.trace.propagatingTags {
		if err := isValidPropagatableTag(k, v); err != nil {
			log.Warn("Won't propagate tag '%s': %v", k, err.Error())
			ctx.trace.setTag(keyPropagationError, "encoding_error")
			continue
		}
		if sb.Len()+len(k)+len(v) > p.cfg.MaxTagsHeaderLen {
			sb.Reset()
			log.Warn("Won't propagate tag: maximum trace tags header len (%d) reached.", p.cfg.MaxTagsHeaderLen)
			ctx.trace.setTag(keyPropagationError, "inject_max_size")
			break
		}
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(v)
	}
	return sb.String()
}

func (p *propagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch c := carrier.(type) {
	case TextMapReader:
		return p.extractTextMap(c)
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
		case p.cfg.TraceHeader:
			ctx.traceID, err = parseUint64(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case p.cfg.ParentHeader:
			ctx.spanID, err = parseUint64(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case p.cfg.PriorityHeader:
			priority, err := strconv.Atoi(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
			ctx.setSamplingPriority(priority, samplernames.Unknown)
		case originHeader:
			ctx.origin = v
		case traceTagsHeader:
			unmarshalPropagatingTags(&ctx, v)
		default:
			if strings.HasPrefix(key, p.cfg.BaggagePrefix) {
				ctx.setBaggageItem(strings.TrimPrefix(key, p.cfg.BaggagePrefix), v)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ctx.traceID == 0 || (ctx.spanID == 0 && ctx.origin != "synthetics") {
		return nil, ErrSpanContextNotFound
	}
	return &ctx, nil
}

// unmarshalPropagatingTags unmarshals tags from v into ctx
func unmarshalPropagatingTags(ctx *spanContext, v string) {
	if ctx.trace == nil {
		ctx.trace = newTrace()
	}
	ctx.trace.mu.Lock()
	defer ctx.trace.mu.Unlock()
	if len(v) > propagationExtractMaxSize {
		log.Warn("Did not extract %s, size limit exceeded: %d. Incoming tags will not be propagated further.", traceTagsHeader, propagationExtractMaxSize)
		ctx.trace.setTag(keyPropagationError, "extract_max_size")
		return
	}
	var err error
	ctx.trace.propagatingTags, err = parsePropagatableTraceTags(v)
	if err != nil {
		log.Warn("Did not extract %s: %v. Incoming tags will not be propagated further.", traceTagsHeader, err.Error())
		ctx.trace.setTag(keyPropagationError, "decoding_error")
	}
}

// setPropagatingTag adds the key value pair to the map of propagating tags on the trace,
// creating the map if one is not initialized.
func setPropagatingTag(ctx *spanContext, k, v string) {
	if ctx.trace == nil {
		// extractors initialize a new spanContext, so the trace might be nil
		ctx.trace = newTrace()
	}
	ctx.trace.mu.Lock()
	defer ctx.trace.mu.Unlock()
	if ctx.trace.propagatingTags == nil {
		ctx.trace.propagatingTags = make(map[string]string)
	}
	ctx.trace.propagatingTags[k] = v
}

const (
	b3TraceIDHeader = "x-b3-traceid"
	b3SpanIDHeader  = "x-b3-spanid"
	b3SampledHeader = "x-b3-sampled"
)

// propagatorB3 implements Propagator and injects/extracts span contexts
// using B3 headers. Only TextMap carriers are supported.
type propagatorB3 struct{}

func (p *propagatorB3) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case TextMapWriter:
		return p.injectTextMap(spanCtx, c)
	default:
		return ErrInvalidCarrier
	}
}

func (*propagatorB3) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}
	writer.Set(b3TraceIDHeader, fmt.Sprintf("%016x", ctx.traceID))
	writer.Set(b3SpanIDHeader, fmt.Sprintf("%016x", ctx.spanID))
	if p, ok := ctx.samplingPriority(); ok {
		if p >= ext.PriorityAutoKeep {
			writer.Set(b3SampledHeader, "1")
		} else {
			writer.Set(b3SampledHeader, "0")
		}
	}
	return nil
}

func (p *propagatorB3) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch c := carrier.(type) {
	case TextMapReader:
		return p.extractTextMap(c)
	default:
		return nil, ErrInvalidCarrier
	}
}

func (*propagatorB3) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var ctx spanContext
	err := reader.ForeachKey(func(k, v string) error {
		var err error
		key := strings.ToLower(k)
		switch key {
		case b3TraceIDHeader:
			if len(v) > 16 {
				v = v[len(v)-16:]
			}
			ctx.traceID, err = strconv.ParseUint(v, 16, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case b3SpanIDHeader:
			ctx.spanID, err = strconv.ParseUint(v, 16, 64)
			if err != nil {
				return ErrSpanContextCorrupted
			}
		case b3SampledHeader:
			priority, err := strconv.Atoi(v)
			if err != nil {
				return ErrSpanContextCorrupted
			}
			ctx.setSamplingPriority(priority, samplernames.Unknown)
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

const (
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	w3cTraceIDTag     = "w3cTraceID"
)

// propagatorW3c implements Propagator and injects/extracts span contexts
// using W3C tracecontext/traceparent headers. Only TextMap carriers are supported.
type propagatorW3c struct{}

func (p *propagatorW3c) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case TextMapWriter:
		return p.injectTextMap(spanCtx, c)
	default:
		return ErrInvalidCarrier
	}
}

// injectTextMap propagates span context attributes into the writer,
// in the format of the traceparentHeader and tracestateHeader.
// traceparentHeader encodes W3C Trace Propagation version, 128-bit traceID,
// spanID, and a flags field, which supports 8 unique flags.
// The current specification only supports a single flag called sampled,
// which is equal to 00000001 when no other flag is present.
// tracestateHeader is a comma-separated list of list-members with a <key>=<value> format,
// where each list-member is managed by a vendor or instrumentation library.
func (*propagatorW3c) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}
	flags := ""
	p, ok := ctx.samplingPriority()
	if ok && p >= ext.PriorityAutoKeep {
		flags = "01"
	} else {
		flags = "00"
	}

	var traceID string
	// if previous traceparent is valid, do NOT update the trace ID
	if ctx.trace != nil && ctx.trace.propagatingTags != nil {
		h := strings.Trim(ctx.trace.propagatingTags[traceparentHeader], "\t -")
		if err := validateTraceparent(h); err == nil {
			traceID = h[len("00-") : len("00-")+32]
		} else {
			traceID = fmt.Sprintf("%032x", ctx.traceID)
		}
	}
	writer.Set(traceparentHeader, fmt.Sprintf("00-%s-%016x-%v", traceID, ctx.spanID, flags))
	// if context priority / origin / tags were updated after extraction,
	// or the tracestateHeader doesn't start with `dd=`
	// we need to recreate tracestate
	if ctx.updated ||
		(ctx.trace != nil && ctx.trace.propagatingTags != nil && !strings.HasPrefix(ctx.trace.propagatingTags[tracestateHeader], "dd=")) {
		writer.Set(tracestateHeader, composeTracestate(ctx, p, ctx.trace.propagatingTags[tracestateHeader]))
	} else {
		writer.Set(tracestateHeader, ctx.trace.propagatingTags[tracestateHeader])
	}
	return nil
}

func validateTraceparent(tp string) error {
	var version, flags int
	var spanID uint64
	var traceID string
	n, err := fmt.Sscanf(strings.Trim(tp, "\t -"), "%2d-%32s-%16x-%2d", &version, &traceID, &spanID, &flags)
	if n != 4 || err != nil {
		return ErrSpanContextCorrupted
	}
	//  if the version is 'ff', the traceparent is invalid
	if version == 255 {
		return ErrSpanContextCorrupted
	}
	// if span or trace id is 0, traceparent is invalid
	if spanID == 0 {
		return ErrSpanContextCorrupted
	}
	tID, err := strconv.ParseUint(traceID[16:], 16, 64)
	if err != nil || tID == 0 {
		return ErrSpanContextCorrupted
	}
	return nil
}

// composeTracestate creates a tracestateHeader from the spancontext.
// The Datadog tracing library is only responsible for managing the list member with key dd,
// which holds the values of the sampling decision(`s:<value>`), origin(`o:<origin>`),
// and propagated tags prefixed with `t.`(e.g. _dd.p.usr.id:usr_id tag will become `t.usr.id:usr_id`).
// All tag keys in the list must have all invalid characters replaced with the underscore.
// Invalid key characters include characters outside the ASCII range 0x20 to 0x7E, space, comma and equal sign.
// All tag values in the list must have all invalid characters replaced with the underscore.
// Invalid value characters include characters outside the ASCII range 0x20 to 0x7E, space,
// comma(reserved for tracestate list-member separator), semi-colon(reserved for separator between entries),
// and tilde sign(reserved to represent equals sign).
func composeTracestate(ctx *spanContext, priority int, oldState string) string {
	keyRgx := regexp.MustCompile(",|=|[^\\x20-\\x7E]+")
	valueRgx := regexp.MustCompile(",|;|:|[^\\x20-\\x7E]+")

	var b strings.Builder
	b.WriteString(fmt.Sprintf("dd=s:%d;", priority))
	listLength := 1

	if ctx.origin != "" {
		b.WriteString(fmt.Sprintf("o:%s",
			valueRgx.ReplaceAllString(ctx.origin, "_")))
	}

	for k, v := range ctx.trace.propagatingTags {
		if !strings.HasPrefix(k, "_dd.p.") {
			continue
		}
		// Datadog propagating tags must be appended to the tracestateHeader
		// with the `t.` prefix. Tag value must have all `=` signs replaced with a tilde (`~`).
		tag := fmt.Sprintf("t.%s:%s",
			keyRgx.ReplaceAllString(k[len("_dd.p."):], "_"),
			strings.ReplaceAll(valueRgx.ReplaceAllString(v, "_"), "=", "~"))
		if b.Len()+len(tag) > 256 {
			break
		}
		b.WriteString(";")
		b.WriteString(tag)
	}
	// the old state is split by vendors, must be concatenated with a `,`
	for _, s := range strings.Split(oldState, ",") {
		if strings.HasPrefix(s, "dd=") {
			continue
		}
		listLength++
		// if the resulting tracestateHeader exceeds 32 list-members,
		// remove the rightmost list-member(s)
		if listLength > 32 {
			break
		}
		b.WriteString("," + s)
	}
	return b.String()
}

func (p *propagatorW3c) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch c := carrier.(type) {
	case TextMapReader:
		return p.extractTextMap(c)
	default:
		return nil, ErrInvalidCarrier
	}
}

func (*propagatorW3c) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var parentHeader string
	var stateHeaders []string
	// to avoid parsing tracestate header(s) if traceparent is invalid
	if err := reader.ForeachKey(func(k, v string) error {
		key := strings.ToLower(k)
		switch key {
		case traceparentHeader:
			if parentHeader != "" {
				return ErrSpanContextCorrupted
			}
			parentHeader = v
		case tracestateHeader:
			stateHeaders = append(stateHeaders, v)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var ctx spanContext
	if err := parseTraceparent(&ctx, parentHeader); err != nil {
		return nil, err
	}
	if err := parseTracestate(&ctx, stateHeaders); err != nil {
		return nil, err
	}
	if ctx.traceID == 0 || ctx.spanID == 0 {
		return nil, ErrSpanContextNotFound
	}
	return &ctx, nil
}

// parseTraceparent attempts to parse traceparentHeader which describes the position
// of the incoming request in its trace graph in a portable, fixed-length format.
// The format of the traceparentHeader is `-` separated string with in the
// following format: `version-traceId-spanID-flags`,
// where:
// - version - represents the version of the W3C Tracecontext Propagation format in hex format.
// - traceId - represents the propagated traceID in the format of 32 hex-encoded digits.
// - spanID - represents the propagated spanID in the format of 16 hex-encoded digits.
// - flags - represents the propagated flags in the format of 2 hex-encoded digits, and supports 8 unique flags.
// Example value of HTTP `traceparent` header: `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`,
// Currently, Go tracer doesn't support 128-bit traceIDs, so the full traceID (32 hex-encoded digits) must be
// stored into a field that is accessible from the span’s context. TraceId will be parsed from the least significant 16
// hex-encoded digits into a 64-bit number.
func parseTraceparent(ctx *spanContext, v string) error {
	v = strings.Trim(v, "\t -")
	if len(v) != 55 {
		return ErrSpanContextCorrupted
	}
	var version, flags int
	var traceID string
	_, err := fmt.Sscanf(v, "%2d-%32s-%16x-%2d", &version, &traceID, &ctx.spanID, &flags)
	if err != nil || version == 255 {
		return ErrSpanContextCorrupted
	}
	if ctx.spanID == 0 {
		return ErrSpanContextCorrupted
	}
	if ctx.traceID, err = strconv.ParseUint(traceID[16:], 16, 64); err != nil || ctx.traceID == 0 {
		return ErrSpanContextCorrupted
	}
	// setting trace-id to be used for span context propagation
	setPropagatingTag(ctx, w3cTraceIDTag, traceID)
	ctx.setSamplingPriority(flags&0x1, samplernames.Unknown)
	return nil
}

// parseTracestate attempts to parse tracestateHeader which is a list
// with zero or more comma-separated (,) list-members.
// An example value would be: `vendorname1=opaqueValue1,vendorname2=opaqueValue2,dd=s:1;o:synthetics`,
// Where `dd` list contains values that would be in x-datadog-tags as well as those needed for propagation information.
// The keys to the “dd“ values have been shortened as follows to save space:
// `sampling_priority` = `s`
// `origin` = `o`
// `_dd.p.` prefix = `t.`
func parseTracestate(ctx *spanContext, headers []string) error {
	// if multiple headers are present, they must be combined and stored
	setPropagatingTag(ctx, tracestateHeader, strings.Join(headers, ";"))
	for _, v := range headers {
		list := strings.Split(strings.Trim(v, "\t "), ",")
		for _, s := range list {
			if !strings.HasPrefix(s, "dd=") {
				continue
			}
			dd := strings.Split(s[len("dd="):], ";")
			tags := make(map[string]string)
			for _, val := range dd {
				x := strings.SplitN(val, ":", 2)
				if len(x) != 2 {
					continue
				}
				tags[x[0]] = x[1]
			}
			for k, v := range tags {
				if k == "o" {
					ctx.origin = v
				} else if k == "s" {
					p, err := strconv.Atoi(v)
					if p > 0 && err == nil {
						// priority from traceparent header
						flagPriority, ok := ctx.samplingPriority()
						if !ok {
							return ErrSpanContextCorrupted
						}
						if (flagPriority == 0 && p < 1) || (flagPriority != 0 && p > 0) {
							ctx.setSamplingPriority(p, samplernames.Unknown)
						}
					}
				} else if strings.HasPrefix(k, "t.") {
					k = k[len("t."):]
					v = strings.ReplaceAll(v, "~", "=")
					setPropagatingTag(ctx, "_dd.p."+k, v)
				}
			}
		}
	}
	return nil
}
