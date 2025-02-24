// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"fmt"
	"math"
	"net/http"

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
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/labstack/echo.v4: Configuring Middleware: %#v", cfg)
	spanOpts := make([]tracer.StartSpanOption, 0, 3+len(cfg.tags))
	spanOpts = append(spanOpts, tracer.ServiceName(cfg.serviceName))
	for k, v := range cfg.tags {
		spanOpts = append(spanOpts, tracer.Tag(k, v))
	}
	spanOpts = append(spanOpts,
		tracer.Tag(ext.Component, instrumentation.PackageLabstackEchoV4),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// If we have an ignoreRequestFunc, use it to see if we proceed with tracing
			if cfg.ignoreRequestFunc != nil && cfg.ignoreRequestFunc(c) {
				return next(c)
			}

			request := c.Request()
			route := c.Path()
			resource := request.Method + " " + route
			opts := options.Copy(spanOpts) // opts must be a copy of spanOpts, locally scoped, to avoid races.
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			opts = append(opts,
				tracer.ResourceName(resource),
				tracer.Tag(ext.HTTPRoute, route),
				httptrace.HeaderTagsFromRequest(request, cfg.headerTags))

			var finishOpts []tracer.FinishOption
			if cfg.noDebugStack {
				finishOpts = []tracer.FinishOption{tracer.NoDebugStack()}
			}

			span, ctx, finishSpans := httptrace.StartRequestSpan(request, opts...)

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			if instr.AppSecEnabled() {
				next = withAppSec(next, span)
			}
			// serve the request to the next middleware
			err := next(c)
			var echoStatus int
			if err != nil && !shouldIgnoreError(cfg, err) {
				// It is impossible to determine what the final status code of a request is in echo.
				// This is the best we can do.
				if echoErr, ok := cfg.translateError(err); ok {
					if cfg.isStatusError(echoErr.Code) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					echoStatus = echoErr.Code

				} else {
					// Any error that is not an *echo.HTTPError will be treated as an error with 500 status code.
					if cfg.isStatusError(500) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					echoStatus = 500
				}
			} else if status := c.Response().Status; status > 0 {
				if cfg.isStatusError(status) {
					if statusErr := errorFromStatusCode(status); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				echoStatus = status
			} else {
				if cfg.isStatusError(200) {
					if statusErr := errorFromStatusCode(200); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				echoStatus = 200
			}
			defer func() {
				finishSpans(echoStatus, func(status int) bool {
					if cfg.isStatusError(status) {
						if statusErr := errorFromStatusCode(status); !shouldIgnoreError(cfg, statusErr) {
							return true
						}
					}
					return false
				}, finishOpts...)
			}()
			return err
		}
	}
}

func errorFromStatusCode(statusCode int) error {
	return fmt.Errorf("%d: %s", statusCode, http.StatusText(statusCode))
}

func shouldIgnoreError(cfg *config, err error) bool {
	return cfg.errCheck != nil && !cfg.errCheck(err)
}
