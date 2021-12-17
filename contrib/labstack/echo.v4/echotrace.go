// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"math"
	"net"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/labstack/echo/v4"
)

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		if appsec.Enabled() {
			next = withAppSec(next)
		}
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

func withAppSec(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		span, ok := tracer.SpanFromContext(req.Context())
		if !ok {
			return next(c)
		}

		httpsec.SetAppSecTags(span)
		params := make(map[string]string)
		for _, n := range c.ParamNames() {
			params[n] = c.Param(n)
		}
		args := httpsec.MakeHandlerOperationArgs(req, params)
		op := httpsec.StartOperation(args, nil)

		defer func() {
			events := op.Finish(httpsec.HandlerOperationRes{Status: c.Response().Status})
			if len(events) > 0 {
				remoteIP, _, err := net.SplitHostPort(req.RemoteAddr)
				if err != nil {
					remoteIP = req.RemoteAddr
				}
				httpsec.SetSecurityEventTags(span, events, remoteIP, args.Headers)
			}
		}()
		return next(c)
	}
}
