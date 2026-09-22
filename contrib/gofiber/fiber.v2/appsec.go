// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
)

const appsecFramework = "github.com/gofiber/fiber/v2"

// useAppSec runs next under AppSec monitoring. It returns the handler error for
// span tagging and whether the response is already handled. A block suppresses
// the handler error so Fiber cannot replace the blocking response.
func useAppSec(c *fiber.Ctx, span trace.TagSetter, next func() error) (err error, handledResponse bool) {
	fctx := c.Context()
	req := convertRequest(c.UserContext(), fctx)

	w := &responseWriter{fctx: fctx}
	_, tr, afterHandle, handled := httpsec.BeforeHandle(w, req, span, &httpsec.Config{
		Framework: appsecFramework,
		OnBlock:   []func(){w.discardHandlerResponse},
		// The fiber handler chain writes to the fasthttp response directly
		// rather than through w, so the headers AppSec reports have to be read
		// back from there instead of from w.
		ResponseHeaderCopier: func(http.ResponseWriter) http.Header { return responseHeaders(fctx) },
	})
	// afterHandle reports the response to the WAF and writes any pending
	// blocking response. It is called explicitly below, and deferred as well so
	// that a panicking handler still gets its span tagged.
	finish := sync.OnceFunc(afterHandle)
	defer finish()

	if handled {
		return nil, true
	}

	// Make the operation reachable from the handler chain so that the AppSec
	// SDK (appsec.MonitorParsedHTTPBody and friends) can find it.
	ctx := tr.Context()
	c.SetUserContext(ctx)

	// A late block must take priority over Fiber's error handler, including
	// when the application ignores a blocking SDK error and returns another one.
	var blocked atomic.Bool
	if op, ok := dyngo.FindOperation[httpsec.HandlerOperation](ctx); ok {
		dyngo.OnData(op, func(*actions.BlockHTTP) { blocked.Store(true) })
	}

	err = next()

	// Fiber only matches the route while walking the handler chain, so the
	// route and its parameters reach the WAF here rather than at operation
	// start. A block decided this late is served by afterHandle.
	if route := c.Route(); route != nil && route.Path != "" {
		if blockErr := httpsec.RouteMatched(ctx, route.Path, c.AllParams()); blockErr != nil {
			instr.Logger().Debug("gofiber/fiber.v2: request blocked on route parameters: %s", blockErr.Error())
		}
	}

	if err != nil && !blocked.Load() {
		// Fiber normally renders errors after middleware returns. Render here
		// so the WAF can inspect the final status and headers, then prevent a
		// second call to the error handler in Fiber's outer request handler.
		if c.App().ErrorHandler(c, err) != nil {
			_ = c.SendStatus(http.StatusInternalServerError)
		}
		handledResponse = true
	}

	finish()
	if w.wroteHeader {
		return nil, true
	}
	return err, handledResponse
}

// convertRequest builds the net/http request AppSec expects out of a fasthttp
// one. The body is deliberately left out: the WAF entry point never reads it,
// and copying it in would force the whole body to be buffered on every request.
func convertRequest(ctx context.Context, fctx *fasthttp.RequestCtx) *http.Request {
	// String conversions here are all copies of fasthttp's zero-copy views into
	// the connection buffer, which is reused for a later request. AppSec puts
	// these values on the span, which outlives the request, so aliasing them
	// would let a subsequent request corrupt a reported attack.
	requestURI := string(fctx.RequestURI())
	uri := fctx.URI()
	// Use the same query parser as Fiber. net/url drops parameters containing
	// semicolons or invalid escapes that fasthttp accepts. Re-encoding keeps
	// those values intact when httpsec calls URL.Query(). Never reject the raw
	// path here: doing so would let a malformed escape disable all monitoring.
	// URL contains normalized values; raw-target inspection must use RequestURI.
	query := make(url.Values)
	for k, v := range fctx.QueryArgs().All() {
		query.Add(string(k), string(v))
	}
	u := &url.URL{
		Scheme:   string(uri.Scheme()),
		Host:     string(uri.Host()),
		Path:     string(uri.Path()),
		RawQuery: query.Encode(),
	}

	header := make(http.Header, fctx.Request.Header.Len())
	for k, v := range fctx.Request.Header.All() {
		header.Add(string(k), string(v))
	}

	req := &http.Request{
		Method:     string(fctx.Method()),
		URL:        u,
		RequestURI: requestURI,
		Host:       string(fctx.Host()),
		RemoteAddr: fctx.RemoteAddr().String(),
		Header:     header,
	}
	return req.WithContext(ctx)
}

func responseHeaders(fctx *fasthttp.RequestCtx) http.Header {
	header := make(http.Header, fctx.Response.Header.Len())
	for k, v := range fctx.Response.Header.All() {
		header.Add(string(k), string(v))
	}
	return header
}

// responseWriter adapts a fasthttp response to the net/http interface AppSec
// needs in order to write a blocking response.
type responseWriter struct {
	fctx        *fasthttp.RequestCtx
	header      http.Header
	wroteHeader bool
}

func (w *responseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header, 2)
	}
	return w.header
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	for k, values := range w.header {
		for _, v := range values {
			w.fctx.Response.Header.Add(k, v)
		}
	}
	w.fctx.Response.SetStatusCode(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	w.fctx.Response.AppendBody(b)
	return len(b), nil
}

// Status implements the interface httpsec uses to report the response status
// code. The fiber handler chain bypasses this writer, so the value comes from
// the fasthttp response rather than from what was written here.
func (w *responseWriter) Status() int {
	return w.fctx.Response.StatusCode()
}

// discardHandlerResponse drops whatever the handler already wrote so that a
// blocking response replaces it instead of being appended to it. It is a no-op
// once the blocking response itself has been written, because httpsec runs the
// OnBlock callbacks again after an early block it has already served.
func (w *responseWriter) discardHandlerResponse() {
	if w.wroteHeader {
		return
	}
	w.fctx.Response.ResetBody()
	w.fctx.Response.Header.Reset()
}
