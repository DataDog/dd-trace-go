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
	w := &responseWriter{response: &fctx.Response}
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
// there are no pairs. AppSec then parses the request with net/http, which can
// only find more values, not fewer.
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
	response    *fasthttp.Response
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
			w.response.Header.Add(k, v)
		}
	}
	w.response.SetStatusCode(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	w.response.AppendBody(b)
	return len(b), nil
}

// Status implements the interface httpsec uses to report the response status
// code. The wrapped handler bypasses this writer, so the value comes from the
// fasthttp response rather than from what was written here.
func (w *responseWriter) Status() int {
	return w.response.StatusCode()
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
