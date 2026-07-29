// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package goka

import (
	"github.com/lovoo/goka"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// gokaHeadersCarrier adapts goka.Headers (map[string][]byte) to the text-map
// interfaces used by both APM trace propagation (tracer.Extract/Inject) and Data
// Streams Monitoring (datastreams.ExtractFromBase64Carrier/InjectToBase64Carrier).
// A single carrier serves both because the four interfaces share the same method
// shapes. The carrier shares the underlying map, so Set mutates the goka.Headers
// it wraps.
type gokaHeadersCarrier goka.Headers

var (
	_ tracer.TextMapReader      = gokaHeadersCarrier(nil)
	_ tracer.TextMapWriter      = gokaHeadersCarrier(nil)
	_ datastreams.TextMapReader = gokaHeadersCarrier(nil)
	_ datastreams.TextMapWriter = gokaHeadersCarrier(nil)
)

// ForeachKey implements tracer.TextMapReader and datastreams.TextMapReader.
func (c gokaHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		if err := handler(k, string(v)); err != nil {
			return err
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter and datastreams.TextMapWriter.
func (c gokaHeadersCarrier) Set(key, val string) {
	c[key] = []byte(val)
}

// ExtractSpanContext extracts the span context propagated in a goka message's
// headers, allowing callers to create manual child spans.
func ExtractSpanContext(headers goka.Headers) (*tracer.SpanContext, error) {
	return tracer.Extract(gokaHeadersCarrier(headers))
}
