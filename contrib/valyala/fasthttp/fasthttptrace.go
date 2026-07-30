// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
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
	s, _ := tracer.StartSpanFromContext(fctx, operationName, opts...)
	fctx.SetUserValue(instr.ActiveSpanKey(), s)
	return s
}
