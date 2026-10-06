// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"iter"
	"net/http"
	"net/url"

	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
)

const appsecFramework = "github.com/valyala/fasthttp"

type appsecHandler struct {
	writer  *responseWriter
	op      dyngo.Operation
	finish  func()
	restore func()
}

// beforeHandle keeps operation cleanup separate from response processing. A
// timeout can finish the operation while its worker still uses the context.
func beforeHandle(fctx *fasthttp.RequestCtx, span trace.TagSetter) (*appsecHandler, bool) {
	req := convertRequest(fctx)
	w := &responseWriter{ctx: fctx, response: &fctx.Response}
	_, req, finish, handled := httpsec.BeforeHandle(w, req, span, &httpsec.Config{
		Framework: appsecFramework,
		// net/http parses cookies and queries differently from fasthttp. It
		// rejects some that fasthttp gives to the handler, so the WAF would
		// not see them.
		Cookies:              collectArgs(fctx.Request.Header.Cookies()),
		QueryParams:          collectArgs(fctx.QueryArgs().All()),
		OnBlock:              []func(){w.discardHandlerResponse},
		ResponseHeaderCopier: func(http.ResponseWriter) http.Header { return responseHeaders(w.response) },
	})
	key := dyngo.ContextKey()
	previous := fctx.UserValue(key)
	op, _ := dyngo.FromContext(req.Context())
	fctx.SetUserValue(key, op)
	return &appsecHandler{
		writer: w,
		op:     op,
		finish: finish,
		restore: func() {
			restoreUserValue(fctx, key, previous)
		},
	}, handled
}

// convertRequest builds the net/http request AppSec expects out of a fasthttp
// one. The body is deliberately left out: the WAF entry point never reads it,
// and copying it in would force the whole body to be buffered on every request.
//
// The URL comes from the request target that fasthttp accepted, not from
// url.ParseRequestURI. That function rejects targets which fasthttp serves,
// such as "/%GG". If it were used, AppSec would not inspect such a request
// while the handler still runs.
func convertRequest(fctx *fasthttp.RequestCtx) *http.Request {
	// String conversions here are all copies of fasthttp's zero-copy views into
	// the connection buffer, which is reused for a later request. AppSec puts
	// these values on the span, which outlives the request, so aliasing them
	// would let a subsequent request corrupt a reported attack.
	uri := fctx.URI()
	header := make(http.Header, fctx.Request.Header.Len())
	for k, v := range fctx.Request.Header.All() {
		header.Add(string(k), string(v))
	}

	req := &http.Request{
		Method:     string(fctx.Method()),
		URL:        &url.URL{Path: string(uri.Path()), RawQuery: string(uri.QueryString())},
		RequestURI: string(fctx.RequestURI()),
		Host:       string(fctx.Host()),
		RemoteAddr: fctx.RemoteAddr().String(),
		Header:     header,
	}
	return req.WithContext(fctx)
}

// collectArgs copies fasthttp's decoded key/value pairs. It returns nil if
// there are no pairs. httpsec then parses the converted request with net/http.
// fasthttp accepts more cookie and query syntax than net/http, so in the inputs
// that we tested, net/http then finds no values either. This fallback is
// defensive: if net/http finds a value that fasthttp drops, the WAF still sees
// it.
func collectArgs(all iter.Seq2[[]byte, []byte]) map[string][]string {
	var values map[string][]string
	for k, v := range all {
		if values == nil {
			values = make(map[string][]string)
		}
		key := string(k)
		values[key] = append(values[key], string(v))
	}
	return values
}

func responseHeaders(response *fasthttp.Response) http.Header {
	header := make(http.Header, response.Header.Len())
	for k, v := range response.Header.All() {
		header.Add(string(k), string(v))
	}
	return header
}

// responseWriter adapts a fasthttp response to the net/http interface AppSec
// needs in order to write a blocking response.
type responseWriter struct {
	ctx         *fasthttp.RequestCtx
	response    *fasthttp.Response
	header      http.Header
	wroteHeader bool
	// wroteBody tells that a block response body was written. httpsec can
	// apply a second block (for example, from a response rule that matches
	// the first block response). That block must not append a second body.
	wroteBody bool
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
			w.response.Header.Add(k, v)
		}
	}
	w.response.SetStatusCode(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.wroteBody {
		// A block response was already written. Do not append a second body.
		return len(b), nil
	}
	w.wroteBody = true
	w.response.AppendBody(b)
	return len(b), nil
}

// Status implements the interface httpsec uses to report the response status
// code. The wrapped handler bypasses this writer, so the value comes from the
// fasthttp response rather than from what was written here.
func (w *responseWriter) Status() int {
	return w.response.StatusCode()
}

// Committed implements the interface httpsec uses to check whether it can still
// replace the response. Without this method, httpsec uses Status, which is
// never zero for a fasthttp response.
//
// fasthttp sends the live response only after the handler returns. But if the
// application calls ctx.TimeoutError*, fasthttp sends the timeout response and
// discards the live response. A block written into the live response then does
// not reach the client, so the response is committed.
//
// The timeout wrappers of this package write a block into their separate
// timeout response, not into the live response. They send that response with
// TimeoutErrorWithResponse after AppSec finishes, also if the application
// called TimeoutError* before. That response is never committed. Do not read
// the context for it: the worker can still call TimeoutError* at that time.
// For the live response, the worker is paused or has returned when AppSec
// finishes.
func (w *responseWriter) Committed() bool {
	if w.response != &w.ctx.Response {
		return false
	}
	// Any timeout response counts, also one set before this scope started:
	// fasthttp sends it instead of the live response. fasthttp does not
	// release a timed-out context for reuse, so the response is from this
	// request.
	return w.ctx.LastTimeoutErrorResponse() != nil
}

// discardHandlerResponse drops whatever the handler already wrote so that a
// blocking response replaces it instead of being appended to it. It is a no-op
// once the blocking response itself has been written, because httpsec runs the
// OnBlock callbacks again after an early block it has already served.
func (w *responseWriter) discardHandlerResponse() {
	if w.wroteHeader {
		return
	}
	w.response.ResetBody()
	w.response.Header.Reset()
}
