// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/labstack/echo"
)

const spanKey = "dd-trace-span"

// Middleware returns middleware that will trace incoming requests.
func Middleware(service string) echo.MiddlewareFunc {
	// TODO(gbbr): Handle this when we switch to OpenTracing.
	t := tracer.DefaultTracer
	t.SetServiceInfo(service, "labstack/echo", ext.AppTypeWeb)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// bail out if tracing isn't enabled
			if !t.Enabled() {
				return next(c)
			}

			request := c.Request()

			resource := c.Path()
			span, ctx := t.NewChildSpanWithContext("http.request", request.Context())
			defer span.Finish()

			span.Service = service
			span.Resource = resource
			span.Type = ext.HTTPType

			span.SetMeta(ext.HTTPMethod, request.Method)
			span.SetMeta(ext.HTTPURL, request.URL.Path)

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			// serve the request to the next middleware
			err := next(c)

			span.SetMeta(ext.HTTPCode, strconv.Itoa(c.Response().Status))

			if err != nil {
				span.SetMeta("echo.errors", err.Error())
				span.SetError(err)
			}

			return err
		}
	}
}
