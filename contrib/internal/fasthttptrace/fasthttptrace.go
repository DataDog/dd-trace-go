// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttptrace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"

	"github.com/valyala/fasthttp"
)

// StartSpanFromContext returns a new span with the given operation name and options.
// If a span is found in the `fctx`, it will be used as the parent of the resulting span.
// The resulting span is then set on the given `fctx`.
// This function is similar to tracer.StartSpanFromContext, but it modifies the given fasthttp context directly.
// If the ChildOf option is passed, it will only be used as the parent if there is no span found in `fctx`.
func StartSpanFromContext(fctx *fasthttp.RequestCtx, operationName string, opts ...tracer.StartSpanOption) tracer.Span {
	s, _ := tracer.StartSpanFromContext(fctx, operationName, opts...)
	fctx.SetUserValue(internal.ActiveSpanKey, s)
	return s
}
