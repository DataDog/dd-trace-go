// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin // import "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

const componentName = "gin-gonic/gin"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGin)
}

// Middleware returns middleware that will trace incoming requests. If service is empty then the
// default service name will be used.
func Middleware(service string, opts ...Option) gin.HandlerFunc {
	cfg := newConfig(service)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/gin-gonic/gin: Configuring Middleware: Service: %s, %#v", cfg.serviceName, cfg)
	spanOpts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	}
	return func(c *gin.Context) {
		if cfg.ignoreRequest(c) {
			return
		}
		route := c.FullPath()
		opts := options.Expand(spanOpts, 0, 4) // opts must be a copy of spanOpts, locally scoped, to avoid races.
		opts = append(opts, tracer.ResourceName(cfg.resourceName(c, route)))
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		opts = append(opts, cfg.routeTag(route, c.Request))
		opts = append(opts, httptrace.HeaderTagsFromRequest(c.Request, cfg.headerTags))
		span, ctx, finishSpans := httptrace.StartRequestSpan(c.Request, opts...)
		defer func() {
			finishSpan(cfg, c, finishSpans)
		}()

		// pass the span through the request context
		c.Request = c.Request.WithContext(ctx)

		// Use AppSec if enabled by user
		if instr.AppSecEnabled() {
			useAppSec(c, httptrace.AppSecSpanTagSetter(span, cfg.otelEnabled))
		}

		// serve the request to the next middleware
		c.Next()

		if len(c.Errors) > 0 {
			span.SetTag("gin.errors", c.Errors.String())
		}
	}
}

func (cfg *config) resourceName(c *gin.Context, route string) string {
	if cfg.resourceNamer != nil {
		return cfg.resourceNamer(c)
	}
	if cfg.otelEnabled {
		return httptrace.ServerSpanName(c.Request.Method, route)
	}
	return defaultResourceNamer(c)
}

func (cfg *config) routeTag(route string, req *http.Request) tracer.StartSpanOption {
	if cfg.otelEnabled {
		return httptrace.HTTPEndpointTag(route, req)
	}
	return tracer.Tag(ext.HTTPRoute, route)
}

func finishSpan(cfg *config, c *gin.Context, finishSpans httptrace.FinishSpanFunc) {
	status := c.Writer.Status()
	statusError := cfg.isStatusError(status)
	var finishOpts []tracer.FinishOption
	if cfg.useGinErrors && statusError && len(c.Errors) > 0 {
		finishOpts = append(finishOpts, tracer.WithError(errors.New(c.Errors.String())))
	}
	finishSpans(status, func(int) bool { return statusError }, finishOpts...)
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span, _ := tracer.StartSpanFromContext(c.Request.Context(), "gin.render.html")
	span.SetTag("go.template", name)
	span.SetTag(ext.Component, componentName)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			span.Finish(tracer.WithError(err))
			panic(r)
		}
		span.Finish()
	}()
	c.HTML(code, name, obj)
}
