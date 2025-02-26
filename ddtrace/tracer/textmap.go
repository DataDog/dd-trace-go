// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
