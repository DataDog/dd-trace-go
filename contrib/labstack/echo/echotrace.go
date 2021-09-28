// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"math"
	"strconv"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/log"

	"github.com/labstack/echo"
)

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		cfg := new(config)
		defaults(cfg)
		for _, fn := range opts {
			fn(cfg)
		}
		log.Debug("contrib/labstack/echo: Configuring Middleware: %#v", cfg)
		return func(c echo.Context) error {
			request := c.Request()
			resource := request.Method + " " + c.Path()
			opts := []ddtrace.StartSpanOption{
				tracer.ServiceName(cfg.serviceName),
				tracer.ResourceName(resource),
				tracer.SpanType(ext.SpanTypeWeb),
				tracer.Tag(ext.HTTPMethod, request.Method),
				tracer.Tag(ext.HTTPURL, request.URL.Path),
				tracer.Measured(),
			}

			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(request.Header)); err == nil {
				opts = append(opts, tracer.ChildOf(spanctx))
			}
			span, ctx := tracer.StartSpanFromContext(request.Context(), "http.request", opts...)
			defer span.Finish()

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			// serve the request to the next middleware
			err := next(c)
			if err != nil {
				span.SetTag(ext.Error, err)
				// invokes the registered HTTP error handler
				c.Error(err)
			}

			span.SetTag(ext.HTTPCode, strconv.Itoa(c.Response().Status))
			return err
		}
	}
}
