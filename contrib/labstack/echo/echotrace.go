// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"math"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/labstack/echo"
)

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/labstack/echo: Configuring Middleware: %#v", cfg)
	spanOpts := []ddtrace.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
		tracer.Tag(ext.Component, "labstack/echo"),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			request := c.Request()
			resource := request.Method + " " + c.Path()
			opts := append(spanOpts, tracer.ResourceName(resource))

			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}

			var finishOpts []tracer.FinishOption
			if cfg.noDebugStack {
				finishOpts = []tracer.FinishOption{tracer.NoDebugStack()}
			}

			span, ctx := httptrace.StartRequestSpan(request, opts...)
			defer func() {
				httptrace.FinishRequestSpan(span, c.Response().Status, finishOpts...)
			}()

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			// serve the request to the next middleware
			err := next(c)
			if err != nil {
				finishOpts = append(finishOpts, tracer.WithError(err))
				// invokes the registered HTTP error handler
				c.Error(err)

				// It is impossible to determine what the final status code of a request is in echo.
				// This is the best we can do.
				switch err := err.(type) {
				case *echo.HTTPError:
					if err.Code >= 500 {
						span.SetTag(ext.Error, err)
					}
					span.SetTag(ext.HTTPCode, strconv.Itoa(err.Code))
				default:
					// Any non-HTTPError errors appear as 5xx errors.
					span.SetTag(ext.Error, err)
					span.SetTag(ext.HTTPCode, "500")
				}
			} else {
				span.SetTag(ext.HTTPCode, "200")
			}
			return err
		}
	}
}
