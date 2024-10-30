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
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/labstack/echo/v4"
)

const componentName = "labstack/echo.v4"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/labstack/echo/v4")
}

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/labstack/echo.v4: Configuring Middleware: %#v", cfg)
	spanOpts := make([]ddtrace.StartSpanOption, 0, 3+len(cfg.tags))
	spanOpts = append(spanOpts, tracer.ServiceName(cfg.serviceName))
	for k, v := range cfg.tags {
		spanOpts = append(spanOpts, tracer.Tag(k, v))
	}
	spanOpts = append(spanOpts,
		tracer.Tag(ext.Component, componentName),
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
			opts := options.Copy(spanOpts...) // opts must be a copy of spanOpts, locally scoped, to avoid races.
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

			span, ctx := httptrace.StartRequestSpan(request, opts...)
			defer func() {
				span.Finish(finishOpts...)
			}()

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			if appsec.Enabled() {
				next = withAppSec(next, span)
			}
			// serve the request to the next middleware
			err := next(c)
			if err != nil && !shouldIgnoreError(cfg, err) {
				// It is impossible to determine what the final status code of a request is in echo.
				// This is the best we can do.
				if echoErr, ok := cfg.translateError(err); ok {
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
					if statusErr := errorFromStatusCode(status); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				span.SetTag(ext.HTTPCode, strconv.Itoa(status))
			} else {
				if cfg.isStatusError(200) {
					if statusErr := errorFromStatusCode(200); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				span.SetTag(ext.HTTPCode, "200")
			}
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
