// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/labstack/echo/v4"
)

func withAppSec(next echo.HandlerFunc, span tracer.Span) echo.HandlerFunc {
	return func(c echo.Context) error {
		params := make(map[string]string)
		for _, n := range c.ParamNames() {
			params[n] = c.Param(n)
		}
		var err error
		// Wrap the echo response to allow monitoring of the response status code in httpsec.WrapHandler()
		srw := &statusResponseWriter{r: c.Response()}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.SetRequest(r)
			err = next(c)
			srw.status = c.Response().Status
			if e, ok := err.(*echo.HTTPError); ok && e != nil {
				srw.status = e.Code
			}
		})
		httpsec.WrapHandler(handler, span, params).ServeHTTP(srw, c.Request())
		return err
	}

}

// statusResponseWriter wraps an echo response to allow tracking/retrieving its status code through a Status() method
// without having to rely on the echo error handlers
type statusResponseWriter struct {
	r      *echo.Response
	status int
}

// Status returns the status code of the response
func (w *statusResponseWriter) Status() int { return w.status }

// WriteHeader wraps the underlying echo response writer WriteHeader() call
func (w *statusResponseWriter) WriteHeader(statusCode int) {
	w.r.WriteHeader(statusCode)
	w.status = statusCode
}

// WriteHeader wraps the underlying echo response writer Write() call
func (w *statusResponseWriter) Write(b []byte) (n int, err error) {
	n, err = w.r.Write(b)
	w.status = w.r.Status
	return n, err
}

// Header wraps the underlying echo response writer Header() call
func (w *statusResponseWriter) Header() http.Header { return w.r.Header() }
