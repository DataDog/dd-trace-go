// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

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
		return func(c echo.Context) error {
			request := c.Request()
			resource := request.Method + " " + c.Path()
			opts := []ddtrace.StartSpanOption{
				tracer.ServiceName(cfg.serviceName),
				tracer.ResourceName(resource),
				tracer.SpanType(ext.SpanTypeWeb),
				tracer.Tag(ext.HTTPMethod, request.Method),
				tracer.Tag(ext.HTTPURL, request.URL.Path),
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

			span.SetTag(ext.HTTPCode, strconv.Itoa(c.Response().Status))
			if err != nil {
				span.SetTag(ext.Error, err)
			}
			return err
		}
	}
}
