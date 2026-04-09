// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"errors"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/labstack/echo/v5"
)

func withAppSec(next echo.HandlerFunc, span trace.TagSetter) echo.HandlerFunc {
	return func(c *echo.Context) error {
		params := make(map[string]string)
		for _, pv := range c.PathValues() {
			params[pv.Name] = pv.Value
		}
		var err error
		handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			c.SetRequest(r)
			err = next(c)
			// If the error is a monitoring one, it means appsec actions will take care of writing the response
			// and handling the error. Don't call the echo error handler in this case.
			if _, ok := err.(*events.BlockingSecurityEvent); ok {
				return
			}
			if err != nil {
				// In echo v5 there is no c.Error() method. We must write the error
				// status code directly so httpsec sees the correct response status
				// and the response is committed before echo's error handler runs.
				// Only write if the response hasn't been committed yet (the handler
				// or a middleware may have already written a response body).
				if r, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && !r.Committed {
					code := http.StatusInternalServerError
					var sc echo.HTTPStatusCoder
					if errors.As(err, &sc) {
						if tmp := sc.StatusCode(); tmp != 0 {
							code = tmp
						}
					}
					c.JSON(code, map[string]string{"message": http.StatusText(code)})
				}
				return
			}
		})
		// Wrap the echo response to allow monitoring of the response status code in httpsec.WrapHandler()
		httpsec.WrapHandler(handler, span, &httpsec.Config{
			Framework:   "github.com/labstack/echo/v5",
			Route:       c.Path(),
			RouteParams: params,
		}).ServeHTTP(&statusResponseWriter{c.Response()}, c.Request())
		// If an error occurred, wrap it under an echo.HTTPError. We need to do this so that APM doesn't override
		// the response code tag with 500 in case it doesn't recognize the error type.
		if _, ok := err.(*echo.HTTPError); !ok && err != nil {
			// We call the echo error handlers in our wrapper when an error occurs, so we know that the response
			// status won't change anymore at this point in the execution
			status := responseStatus(c)
			if status == 0 {
				status = http.StatusInternalServerError
			}
			err = echo.NewHTTPError(status, err.Error())
		}
		return err
	}
}

// statusResponseWriter wraps an http.ResponseWriter to allow tracking/retrieving its status code through a Status() method
// without having to rely on the echo error handlers
type statusResponseWriter struct {
	http.ResponseWriter
}

// Status returns the status code of the response
func (w *statusResponseWriter) Status() int {
	if r, err := echo.UnwrapResponse(w.ResponseWriter); err == nil {
		return r.Status
	}
	return 0
}

// Unwrap returns the underlying http.ResponseWriter so that echo.UnwrapResponse can work through the chain.
func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// appsecBinder wraps an echo.Binder to monitor parsed request bodies for AppSec.
// In echo v5, Context is a struct (not an interface), so we can't override Bind()
// on the context itself. Instead, we wrap the Binder at the Echo instance level.
type appsecBinder struct {
	inner echo.Binder
}

func (b *appsecBinder) Bind(c *echo.Context, target any) error {
	err := b.inner.Bind(c, target)
	if err == nil {
		err = appsec.MonitorParsedHTTPBody(c.Request().Context(), target)
	}
	return err
}
