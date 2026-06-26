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

func withAppSec(next echo.HandlerFunc, span trace.TagSetter, e *echo.Echo) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var params map[string]string
		if pvs := c.PathValues(); len(pvs) > 0 {
			params = make(map[string]string, len(pvs))
			for _, pv := range pvs {
				params[pv.Name] = pv.Value
			}
		}
		var err error
		handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			c.SetRequest(r)
			err = next(c)
			// If the error is a monitoring one, AppSec will take care of writing
			// the response. Skip our error handling entirely.
			if _, ok := err.(*events.BlockingSecurityEvent); ok {
				return
			}
			if err == nil {
				return
			}
			// In echo v5 there is no c.Error() method. We must commit the
			// response with the correct status code before httpsec reads it.
			// Only commit if the response hasn't already been written by the
			// handler or a downstream middleware.
			if r, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && !r.Committed {
				// Prefer the user-configured HTTPErrorHandler when available so
				// custom error renderers are honored; fall back to a minimal
				// JSON response otherwise.
				if e != nil && e.HTTPErrorHandler != nil {
					e.HTTPErrorHandler(c, err)
				} else {
					code := http.StatusInternalServerError
					var sc echo.HTTPStatusCoder
					if errors.As(err, &sc) {
						if tmp := sc.StatusCode(); tmp != 0 {
							code = tmp
						}
					}
					if jerr := c.JSON(code, map[string]string{"message": http.StatusText(code)}); jerr != nil {
						instr.Logger().Debug("contrib/labstack/echo.v5: failed to write fallback error response: %v", jerr)
					}
				}
			}
		})
		// Wrap the echo response to allow monitoring of the response status code in httpsec.WrapHandler()
		httpsec.WrapHandler(handler, span, &httpsec.Config{
			Framework:   "github.com/labstack/echo/v5",
			Route:       c.Path(),
			RouteParams: params,
		}).ServeHTTP(&statusResponseWriter{c.Response()}, c.Request())
		// If an error occurred, wrap it under an echo.HTTPError so APM doesn't
		// override the response code tag with 500 when it doesn't recognize the
		// error type. By this point the inner http.HandlerFunc has either
		// committed the response (via e.HTTPErrorHandler or our JSON fallback)
		// or the response was already committed by the handler, so the status
		// recorded on the echo Response is final.
		if _, ok := err.(*echo.HTTPError); !ok && err != nil {
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
//
// In echo v5, Context is a concrete struct (not an interface), so we cannot
// override Bind() at the Context layer like the v4 integration did. The
// Binder is the only hook point left for body interception, so AppSec wraps
// it at the Echo instance level. The previously-installed Binder is captured
// as `inner` and runs first on every Bind call; the chain is preserved so
// users can keep their custom Binder logic.
//
// Breakage: this wrap is order-sensitive — if [echo.Echo.Binder] is reassigned
// after the wrap has been installed (e.g. after the first request), the
// captured `inner` is orphaned and AppSec body monitoring stops. Echo itself
// forbids mutating instance fields after the server has started, so this
// falls under the same restriction.
type appsecBinder struct {
	inner echo.Binder
}

func (b *appsecBinder) Bind(c *echo.Context, target any) error {
	if b.inner != nil {
		if err := b.inner.Bind(c, target); err != nil {
			return err
		}
	}
	return appsec.MonitorParsedHTTPBody(c.Request().Context(), target)
}
