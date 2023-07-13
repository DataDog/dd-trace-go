// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gearbox provides functions to trace the gogearbox/gearbox package (https://github.com/gogearbox/gearbox)
package gearbox // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox"

import (
	"fmt"
	"strconv"
	"strings"

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

func Middleware(opts ...Option) func(gctx gearbox.Context) {
	cfg := newConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/gogearbox/gearbox: Configuring Middleware: Service: %#v", cfg)
	spanOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
	}
	return func(gctx gearbox.Context) {
		if cfg.ignoreRequest(gctx) {
			gctx.Next()
			return
		}
		fctx := gctx.Context()
		spanOpts = defaultSpanTags(spanOpts, fctx)
		// Create an instance of FasthttpContextCarrier, which embeds *fasthttp.RequestCtx and implements TextMapReader
		fcc := &FasthttpContextCarrier{
			reqCtx: fctx,
		}
		if sctx, err := tracer.Extract(fcc); err == nil {
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}
		span, _ := tracer.StartSpanFromContext(fctx, "http.request", spanOpts...)
		defer span.Finish()

		fctx.SetUserValue(tracer.GetActiveSpanKey(), span)

		gctx.Next()

		span.SetTag(ext.ResourceName, cfg.resourceNamer(gctx))

		// TODO: Implement config for users to define error status codes
		status := fctx.Response.StatusCode()
		if cfg.isStatusError(status) {
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, string(fctx.Response.Body())))
		}
		span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	}
}

// MTOFF: Does it matter when these span tags are added?
// other integrations have some tags added after startSpan/before FinishSpan,
// whereas I'm adding as many as possible before startSpan, since none of these depend on operations that happen further down the req chain AFAICT
func defaultSpanTags(opts []tracer.StartSpanOption, ctx *fasthttp.RequestCtx) []tracer.StartSpanOption {
	opts = append([]ddtrace.StartSpanOption{
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, string(ctx.Method())),
		tracer.Tag(ext.HTTPURL, string(ctx.URI().FullURI())),
		tracer.Tag(ext.HTTPUserAgent, string(ctx.UserAgent())),
		tracer.Measured(),
	}, opts...)
	if host := string(ctx.Host()); len(host) > 0 {
		opts = append([]ddtrace.StartSpanOption{tracer.Tag("http.host", host)}, opts...)
	}
	return opts
}

// MTOFF: This will be useful if we add a fasthttp integration. Might also be useful for the fiber integration.
// But should the implement it be separated into a gearboxutils pkg? Really it's a fashttp util....

// FasthttpContextCarrier implements tracer.TextMapWriter and tracer.TextMapReader on top
// of fasthttp's RequestHeader object, allowing it to be used as a span context carrier for
// distributed tracing.
type FasthttpContextCarrier struct {
	reqCtx *fasthttp.RequestCtx
}

// ForeachKey iterates over fasthttp request header keys and values
func (f *FasthttpContextCarrier) ForeachKey(handler func(key, val string) error) error {
	reqHeader := &f.reqCtx.Request.Header
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

// Set adds the given value to request header for key. Key will be lowercased to match
// the metadata implementation.
func (f *FasthttpContextCarrier) Set(key, val string) {
	k := strings.ToLower(key)
	f.reqCtx.Request.Header.Set(k, val)
}

// Get will return the first entry in the metadata at the given key.
func (f *FasthttpContextCarrier) Get(key string) string {
	return string(f.reqCtx.Request.Header.Peek(key))
}
