// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/labstack/echo/v4"
)

func withAppSec(next echo.HandlerFunc, span trace.TagSetter) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Hijack the context with monitoring methods...
		c = appsecContext{c}

		params := make(map[string]string)
		for _, n := range c.ParamNames() {
			params[n] = c.Param(n)
		}
		var err error
		handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			c.SetRequest(r)
			err = next(c)
			// If the error is a monitoring one, it means appsec actions will take care of writing the response
			// and handling the error. Don't call the echo error handler in this case
			if _, ok := err.(*events.BlockingSecurityEvent); !ok && err != nil {
				c.Error(err)
			}
		})
		// Wrap the echo response to allow monitoring of the response status code in httpsec.WrapHandler()
		httpsec.WrapHandler(handler, span, &httpsec.Config{
			Framework:   "github.com/labstack/echo/v4",
			Route:       c.Path(),
			RouteParams: params,
		}).ServeHTTP(&statusResponseWriter{Response: c.Response()}, c.Request())
		// If an error occurred, wrap it under an echo.HTTPError. We need to do this so that APM doesn't override
		// the response code tag with 500 in case it doesn't recognize the error type.
		if _, ok := err.(*echo.HTTPError); !ok && err != nil {
			// We call the echo error handlers in our wrapper when an error occurs, so we know that the response
			// status won't change anymore at this point in the execution
			err = echo.NewHTTPError(c.Response().Status, err.Error())
		}
		return err
	}

}

// statusResponseWriter wraps an echo response to allow tracking/retrieving its status code through a Status() method
// without having to rely on the echo error handlers
type statusResponseWriter struct {
	*echo.Response
}

// Status returns the status code of the response
func (w *statusResponseWriter) Status() int {
	return w.Response.Status
}

type appsecContext struct {
	echo.Context
}

func (c appsecContext) Bind(in any) error {
	err := c.Context.Bind(in)
	if err == nil {
		err = appsec.MonitorParsedHTTPBody(c.Request().Context(), in)
	}
	return err
}

func (c appsecContext) JSON(code int, i any) error {
	if err := appsec.MonitorHTTPResponseBody(c.Request().Context(), i); err != nil {
		return err
	}
	return c.Context.JSON(code, i)
}

func (c appsecContext) JSONPretty(code int, i any, indent string) error {
	if err := appsec.MonitorHTTPResponseBody(c.Request().Context(), i); err != nil {
		return err
	}
	return c.Context.JSONPretty(code, i, indent)
}

func (c appsecContext) JSONP(code int, callback string, i any) error {
	if err := appsec.MonitorHTTPResponseBody(c.Request().Context(), i); err != nil {
		return err
	}
	return c.Context.JSONP(code, callback, i)
}

func (c appsecContext) XML(code int, i any) error {
	if err := appsec.MonitorHTTPResponseBody(c.Request().Context(), i); err != nil {
		return err
	}
	return c.Context.XML(code, i)
}

func (c appsecContext) XMLPretty(code int, i any, indent string) error {
	if err := appsec.MonitorHTTPResponseBody(c.Request().Context(), i); err != nil {
		return err
	}
	return c.Context.XMLPretty(code, i, indent)
}
