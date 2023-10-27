// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package fasthttp provides functions to trace the valyala/fasthttp package (https://github.com/valyala/fasthttp)
package fasthttp // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/valyala/fasthttp.v1"

import (
	"fmt"
	"strconv"

	"github.com/valyala/fasthttp"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/fasthttptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "valyala/fasthttp.v1"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// WrapHandler wraps a fasthttp.RequestHandler with tracing middleware
func WrapHandler(h fasthttp.RequestHandler, opts ...Option) fasthttp.RequestHandler {
	cfg := newConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/valyala/fasthttp.v1: Configuring Middleware: cfg: %#v", cfg)
	spanOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
	}
	return func(fctx *fasthttp.RequestCtx) {
		if cfg.ignoreRequest(fctx) {
			h(fctx)
			return
		}
		spanOpts = append(spanOpts, defaultSpanOptions(fctx)...)
		fcc := &fasthttptrace.HTTPHeadersCarrier{
			ReqHeader: &fctx.Request.Header,
		}
		if sctx, err := tracer.Extract(fcc); err == nil {
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}
		span := fasthttptrace.StartSpanFromContext(fctx, "http.request", spanOpts...)
		defer span.Finish()
		h(fctx)
		span.SetTag(ext.ResourceName, cfg.resourceNamer(fctx))
		status := fctx.Response.StatusCode()
		if cfg.isStatusError(status) {
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, string(fctx.Response.Body())))
		}
		span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	}
}

func defaultSpanOptions(fctx *fasthttp.RequestCtx) []tracer.StartSpanOption {
	opts := []ddtrace.StartSpanOption{
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, string(fctx.Method())),
		tracer.Tag(ext.HTTPURL, string(fctx.URI().FullURI())),
		tracer.Tag(ext.HTTPUserAgent, string(fctx.UserAgent())),
		tracer.Measured(),
	}
	if host := string(fctx.Host()); len(host) > 0 {
		opts = append(opts, tracer.Tag("http.host", host))
	}
	return opts
}
