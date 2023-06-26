// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gearbox provides functions to trace the gogearbox/gearbox package (https://github.com/gogearbox/gearbox)
package gearbox // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox"

import (
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/gogearbox/gearbox"
	"github.com/valyala/fasthttp"
)

const componentName = "gogearbox/gearbox"

func init() {
	telemetry.LoadIntegration(componentName)
}

func Middleware(service string) func(gctx gearbox.Context) {
	cfg := newConfig(service)

	log.Debug("contrib/gogearbox/gearbox: Configuring Middleware: Service: %s", service)

	spanOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
	}
	spanOpts = append(spanOpts, tracer.Tag(ext.Component, componentName), tracer.Tag(ext.SpanKind, ext.SpanKindServer))

	return func(gctx gearbox.Context) {
		fctx := gctx.Context()
		spanOpts = defaultSpanTags(spanOpts, fctx)
		fcc := &FasthttpContextCarrier{
			reqCtx: fctx,
		}
		if sctx, err := tracer.Extract(fcc); err == nil {
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}
		span, _ := tracer.StartSpanFromContext(fctx, "http.request", spanOpts...)
		defer func() {
			httptrace.FinishRequestSpan(span, fctx.Response.StatusCode())
		}()
		fctx.SetUserValue(tracer.GetActiveSpanKey(), span)

		gctx.Next()

		span.SetTag(ext.ResourceName, cfg.resourceNamer(gctx))
	}
}

func defaultSpanTags(opts []tracer.StartSpanOption, ctx *fasthttp.RequestCtx) []tracer.StartSpanOption {
	opts = append([]ddtrace.StartSpanOption{ 
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, string(ctx.Method())),
		tracer.Tag(ext.HTTPURL, string(ctx.URI().FullURI())),
		tracer.Tag(ext.HTTPUserAgent, string(ctx.UserAgent())),
		tracer.Measured(),
	}, opts...)
	if host := string(ctx.Host()); len(host) > 0 {
		opts = append([]ddtrace.StartSpanOption{ tracer.Tag("http.host", host)}, opts...)
	}
	return opts
}

// this will be useful if we add a fasthttp integration
// might also be useful for the fiber integration
type FasthttpContextCarrier struct {
	reqCtx *fasthttp.RequestCtx
}

func (gcc *FasthttpContextCarrier) ForeachKey(handler func(key, val string) error) error {
	reqHeader := &gcc.reqCtx.Request.Header
	keys := reqHeader.PeekKeys()
	for h := range keys {
		header := string(keys[h])
		vals := reqHeader.PeekAll(header)
		for v := range vals {
			if err := handler(header, string(vals[v])); err != nil {
				return err
		}
		}
	}
	return nil
}