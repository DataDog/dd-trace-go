// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"net/http"
	"net/url"

	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
)

const appsecFramework = "github.com/valyala/fasthttp"

// beforeHandle runs the AppSec request-start logic for fctx, adapting it to the
// net/http shaped entry point in httpsec. It returns an afterHandle function,
// which must run once the handler is done and before the span is finished, and
// a handled boolean reporting whether AppSec already wrote a blocking response,
// in which case the wrapped handler must not run.
//
// Both return values are zero when the request could not be adapted, meaning
// AppSec is skipped for it.
func beforeHandle(fctx *fasthttp.RequestCtx, span trace.TagSetter) (afterHandle func(), handled bool) {
	req, err := convertRequest(fctx)
	if err != nil {
		instr.Logger().Debug("contrib/valyala/fasthttp: appsec monitoring skipped for this request: %s", err.Error())
		return nil, false
	}

	w := &responseWriter{fctx: fctx}
	_, _, afterHandle, handled = httpsec.BeforeHandle(w, req, span, &httpsec.Config{
		Framework: appsecFramework,
		OnBlock:   []func(){w.discardHandlerResponse},
		// The wrapped handler writes to the fasthttp response directly rather
		// than through w, so the headers AppSec reports have to be read back
		// from there instead of from w.
		ResponseHeaderCopier: func(http.ResponseWriter) http.Header { return responseHeaders(fctx) },
	})
	return afterHandle, handled
}

// convertRequest builds the net/http request AppSec expects out of a fasthttp
// one. The body is deliberately left out: the WAF entry point never reads it,
// and copying it in would force the whole body to be buffered on every request.
func convertRequest(fctx *fasthttp.RequestCtx) (*http.Request, error) {
	// String conversions here are all copies of fasthttp's zero-copy views into
	// the connection buffer, which is reused for a later request. AppSec puts
	// these values on the span, which outlives the request, so aliasing them
	// would let a subsequent request corrupt a reported attack.
	requestURI := string(fctx.RequestURI())
	u, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return nil, err
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
	return req.WithContext(fctx), nil
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
// code. The wrapped handler bypasses this writer, so the value comes from the
// fasthttp response rather than from what was written here.
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
