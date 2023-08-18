// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gearbox provides functions to trace the gogearbox/gearbox package (https://github.com/gogearbox/gearbox)
package gearbox // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox"

import (
	"fmt"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox.v1/internal/gearboxutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/gogearbox/gearbox"
	"github.com/valyala/fasthttp"
)

const componentName = "gogearbox/gearbox.v1"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// Middleware returns middleware that will trace incoming requests.
func Middleware(opts ...Option) func(gctx gearbox.Context) {
	cfg := newConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/gogearbox/gearbox.v1: Configuring Middleware: cfg: %#v", cfg)
	spanOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
	}
	return func(gctx gearbox.Context) {
		if cfg.ignoreRequest(gctx) {
			gctx.Next()
			return
		}
		fctx := gctx.Context()
		spanOpts = append(spanOpts, defaultSpanOptions(fctx)...)
		// Create an instance of FasthttpCarrier, which embeds *fasthttp.RequestCtx and implements TextMapReader
		fcc := &gearboxutil.FastHTTPHeadersCarrier{
			ReqHeader: &fctx.Request.Header,
		}
		if sctx, err := tracer.Extract(fcc); err == nil {
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}
		span, _ := tracer.StartSpanFromContext(fctx, "http.request", spanOpts...)
		defer span.Finish()

		// AFAICT, there is no automatic way to update the fashttp context with the context returned from tracer.StartSpanFromContext
		// Instead I had to manually add the activeSpanKey onto the fashttp context
		activeSpanKey := tracer.ContextKey{}
		fctx.SetUserValue(activeSpanKey, span)

		gctx.Next()

		span.SetTag(ext.ResourceName, cfg.resourceNamer(gctx))

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
