// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// HTTPHeadersCarrier wraps an http.Header as a TextMapWriter and TextMapReader, allowing
// it to be used using the provided Propagator implementation.
type HTTPHeadersCarrier = v2.HTTPHeadersCarrier

var _ TextMapWriter = (*HTTPHeadersCarrier)(nil)
var _ TextMapReader = (*HTTPHeadersCarrier)(nil)

// TextMapCarrier allows the use of a regular map[string]string as both TextMapWriter
// and TextMapReader, making it compatible with the provided Propagator.
type TextMapCarrier = v2.TextMapCarrier

var _ TextMapWriter = (*TextMapCarrier)(nil)
var _ TextMapReader = (*TextMapCarrier)(nil)

const (
	headerPropagationStyleInject  = "DD_TRACE_PROPAGATION_STYLE_INJECT"
	headerPropagationStyleExtract = "DD_TRACE_PROPAGATION_STYLE_EXTRACT"
	headerPropagationStyle        = "DD_TRACE_PROPAGATION_STYLE"
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

	// DefaultBaggageHeader specifies the key that will be used in HTTP headers
	// or text maps to store the baggage value.
	DefaultBaggageHeader = "baggage"
)

// originHeader specifies the name of the header indicating the origin of the trace.
// It is used with the Synthetics product and usually has the value "synthetics".
const originHeader = "x-datadog-origin"

// traceTagsHeader holds the propagated trace tags
const traceTagsHeader = "x-datadog-tags"

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

	// BaggageHeader specifies the map key that will be used to store the baggage key-value pairs.
	// It defaults to DefaultBaggageHeader.
	BaggageHeader string
}

const (
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
)

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
	c := &v2.PropagatorConfig{
		B3:               cfg.B3,
		BaggagePrefix:    cfg.BaggagePrefix,
		ParentHeader:     cfg.ParentHeader,
		PriorityHeader:   cfg.PriorityHeader,
		MaxTagsHeaderLen: cfg.MaxTagsHeaderLen,
		TraceHeader:      cfg.TraceHeader,
	}
	wrapped := make([]v2.Propagator, len(propagators))
	for i, p := range propagators {
		wrapped[i] = &propagatorV1Adapter{propagator: p}
	}
	p := v2.NewPropagator(c, wrapped...)
	return &propagatorV2Adapter{propagator: p}
}

const (
	baggageMaxItems     = 64
	baggageMaxBytes     = 8192
	safeCharactersKey   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&'*+-.^_`|~"
	safeCharactersValue = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&'()*+-./:<>?@[]^_`{|}~"
)

// encodeKey encodes a key with the specified safe characters
func encodeKey(key string) string {
	return urlEncode(strings.TrimSpace(key), safeCharactersKey)
}

// encodeValue encodes a value with the specified safe characters
func encodeValue(value string) string {
	return urlEncode(strings.TrimSpace(value), safeCharactersValue)
}

// urlEncode performs percent-encoding while respecting the safe characters
func urlEncode(input string, safeCharacters string) string {
	var encoded strings.Builder
	for _, c := range input {
		if strings.ContainsRune(safeCharacters, c) {
			encoded.WriteRune(c)
		} else {
			encoded.WriteString(url.QueryEscape(string(c)))
		}
	}
	return encoded.String()
}

// propagatorBaggage implements Propagator and injects/extracts span contexts
// using baggage headers.
type propagatorBaggage struct{}

func (p *propagatorBaggage) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case TextMapWriter:
		return p.injectTextMap(spanCtx, c)
	default:
		return ErrInvalidCarrier
	}
}

// injectTextMap propagates baggage items from the span context into the writer,
// in the format of a single HTTP "baggage" header. Baggage consists of key=value pairs,
// separated by commas. This function enforces a maximum number of baggage items and a maximum overall size.
// If either limit is exceeded, excess items or bytes are dropped, and a warning is logged.
//
// Example of a single "baggage" header:
// baggage: foo=bar,baz=qux
//
// Each key and value pair is encoded and added to the existing baggage header in <key>=<value> format,
// joined together by commas,
func (*propagatorBaggage) injectTextMap(spanCtx ddtrace.SpanContext, writer TextMapWriter) error {
	ctx, _ := spanCtx.(*spanContext)
	if ctx == nil {
		return nil
	}

	// Copy the baggage map under the read lock to avoid data races.
	ctx.mu.RLock()
	baggageCopy := make(map[string]string, len(ctx.baggage))
	for k, v := range ctx.baggage {
		baggageCopy[k] = v
	}
	ctx.mu.RUnlock()

	// If the baggage is empty, do nothing.
	if len(baggageCopy) == 0 {
		return nil
	}

	baggageItems := make([]string, 0, len(baggageCopy))
	totalSize := 0
	count := 0

	for key, value := range baggageCopy {
		if count >= baggageMaxItems {
			log.Warn("Baggage item limit exceeded. Only the first %d items will be propagated.", baggageMaxItems)
			break
		}

		encodedKey := encodeKey(key)
		encodedValue := encodeValue(value)
		item := fmt.Sprintf("%s=%s", encodedKey, encodedValue)

		itemSize := len(item)
		if count > 0 {
			itemSize++ // account for the comma separator
		}

		if totalSize+itemSize > baggageMaxBytes {
			log.Warn("Baggage size limit exceeded. Only the first %d bytes will be propagated.", baggageMaxBytes)
			break
		}

		baggageItems = append(baggageItems, item)
		totalSize += itemSize
		count++
	}

	if len(baggageItems) > 0 {
		writer.Set("baggage", strings.Join(baggageItems, ","))
	}

	return nil
}

func (p *propagatorBaggage) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	switch c := carrier.(type) {
	case TextMapReader:
		return p.extractTextMap(c)
	default:
		return nil, ErrInvalidCarrier
	}
}

func (*propagatorBaggage) extractTextMap(reader TextMapReader) (ddtrace.SpanContext, error) {
	var baggageHeader string
	var ctx spanContext
	err := reader.ForeachKey(func(k, v string) error {
		if strings.ToLower(k) == "baggage" {
			baggageHeader = v
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	ctx.baggage = make(map[string]string)

	if baggageHeader == "" {
		return &ctx, nil
	}

	pairs := strings.Split(baggageHeader, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if !strings.Contains(pair, "=") {
			// If a pair doesn't contain '=', treat it as invalid.
			return nil, fmt.Errorf("Invalid baggage item: %s", pair)
		}

		keyValue := strings.SplitN(pair, "=", 2)
		rawKey := strings.TrimSpace(keyValue[0])
		rawValue := strings.TrimSpace(keyValue[1])

		decKey, errKey := url.QueryUnescape(rawKey)
		decVal, errVal := url.QueryUnescape(rawValue)
		if errKey != nil || errVal != nil {
			return nil, fmt.Errorf("Invalid baggage item: %s", pair)
		}
		ctx.baggage[decKey] = decVal
	}
	if len(ctx.baggage) > 0 {
		atomic.StoreUint32(&ctx.hasBaggage, 1)
	}
	return &ctx, nil
}
