// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
// WARNING: The underlying v3 version of labstack/echo has known security vulnerabilities that have been resolved in v4
// and is no longer under active development. As such consider this package deprecated.
// It is highly recommended that you update to the latest version available at labstack/echo.v4.
package echo

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/labstack/echo/v4"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageLabstackEchoV4)
}

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...OptionFn) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	instr.Logger().Debug("contrib/labstack/echo: Configuring Middleware: %#v", cfg)
	spanOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
		tracer.Tag(ext.Component, instrumentation.PackageLabstackEchoV4),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			request := c.Request()
			resource := request.Method + " " + c.Path()
			opts := options.Copy(spanOpts) // opts must be a copy of spanOpts, locally scoped, to avoid races.
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			opts = append(opts,
				tracer.ResourceName(resource),
				httptrace.HeaderTagsFromRequest(request, cfg.headerTags))
			// TODO: Should this also have an `http.route` tag like the v4 library does?

			var finishOpts []tracer.FinishOption
			if cfg.noDebugStack {
				finishOpts = []tracer.FinishOption{tracer.NoDebugStack()}
			}

			span, ctx := httptrace.StartRequestSpan(request, opts...)
			defer func() {
				span.Finish(finishOpts...)
			}()

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			// Use AppSec if enabled by user
			if instr.AppSecEnabled() {
				next = withAppSec(next, span)
			}

			// serve the request to the next middleware
			err := next(c)
			if err != nil {
				// It is impossible to determine what the final status code of a request is in echo.
				// This is the best we can do.
				var echoErr *echo.HTTPError
				if errors.As(err, &echoErr) {
					if cfg.isStatusError(echoErr.Code) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					span.SetTag(ext.HTTPCode, strconv.Itoa(echoErr.Code))
				} else {
					// Any error that is not an *echo.HTTPError will be treated as an error with 500 status code.
					if cfg.isStatusError(500) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					span.SetTag(ext.HTTPCode, "500")
				}
			} else if status := c.Response().Status; status > 0 {
				if cfg.isStatusError(status) {
					finishOpts = append(finishOpts, tracer.WithError(fmt.Errorf("%d: %s", status, http.StatusText(status))))
				}
				span.SetTag(ext.HTTPCode, strconv.Itoa(status))
			} else {
				if cfg.isStatusError(200) {
					finishOpts = append(finishOpts, tracer.WithError(fmt.Errorf("%d: %s", 200, http.StatusText(200))))
				}
				span.SetTag(ext.HTTPCode, "200")
			}
			return err
		}
	}
}
