// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gin-gonic/gin"

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gin-gonic/gin"
)

// Middleware returns middleware that will trace incoming requests. If service is empty then the
// default service name will be used.
func Middleware(service string, opts ...Option) gin.HandlerFunc {
	cfg := newConfig(service)
	for _, opt := range opts {
		opt(cfg)
	}
	return func(c *gin.Context) {
		resource := cfg.resourceNamer(c)
		opts := []ddtrace.StartSpanOption{
			tracer.ServiceName(cfg.serviceName),
			tracer.ResourceName(resource),
			tracer.SpanType(ext.SpanTypeWeb),
			tracer.Tag(ext.HTTPMethod, c.Request.Method),
			tracer.Tag(ext.HTTPURL, c.Request.URL.Path),
			tracer.Measured(),
		}
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(c.Request.Header)); err == nil {
			opts = append(opts, tracer.ChildOf(spanctx))
		}
		span, ctx := tracer.StartSpanFromContext(c.Request.Context(), "http.request", opts...)
		defer span.Finish()

		// pass the span through the request context
		c.Request = c.Request.WithContext(ctx)

		// serve the request to the next middleware
		c.Next()

		status := c.Writer.Status()
		span.SetTag(ext.HTTPCode, strconv.Itoa(status))
		if status >= 500 && status < 600 {
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
		}

		if len(c.Errors) > 0 {
			span.SetTag("gin.errors", c.Errors.String())
		}
	}
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span, _ := tracer.StartSpanFromContext(c.Request.Context(), "gin.render.html")
	span.SetTag("go.template", name)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			span.Finish(tracer.WithError(err))
			panic(r)
		} else {
			span.Finish()
		}
	}()
	c.HTML(code, name, obj)
}
