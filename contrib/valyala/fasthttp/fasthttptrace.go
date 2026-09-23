// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/valyala/fasthttp"
)

// StartSpanFromContext returns a new span with the given operation name and options.
// If a span is found in the `fctx`, it will be used as the parent of the resulting span.
// The resulting span is then set on the given `fctx`.
// This function is similar to tracer.StartSpanFromContext, but it modifies the given fasthttp context directly.
// If the ChildOf option is passed, it takes precedence over the span found in `fctx`: a span reaches
// `fctx` through SetUserValue rather than through tracer.ContextWithSpan, so it is an ambient scope
// rather than a parent the caller named, and an explicit ChildOf outranks it.
func StartSpanFromContext(fctx *fasthttp.RequestCtx, operationName string, opts ...tracer.StartSpanOption) *tracer.Span {
	s, _ := tracer.StartSpanFromContext(snapshotSpanContext(fctx), operationName, opts...)
	fctx.SetUserValue(instr.ActiveSpanKey(), s)
	return s
}

// Span.Finish restores profiling labels through the start context. Keep that
// lookup independent of request storage that a timeout worker can still change.
func snapshotSpanContext(fctx *fasthttp.RequestCtx) context.Context {
	var values []spanContextValue
	fctx.VisitUserValuesAll(func(key, value any) {
		values = append(values, spanContextValue{key: key, value: value})
	})
	if len(values) == 0 {
		return context.Background()
	}
	return &spanContextSnapshot{Context: context.Background(), values: values}
}

type spanContextValue struct {
	key, value any
}

type spanContextSnapshot struct {
	context.Context
	values []spanContextValue
}

func (ctx *spanContextSnapshot) Value(key any) any {
	if bytes, ok := key.([]byte); ok {
		key = string(bytes)
	}
	for _, entry := range ctx.values {
		if entry.key == key {
			return entry.value
		}
	}
	return nil
}
