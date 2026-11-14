// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package appsecwire

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"slices"

	"github.com/klauspost/compress/gzhttp"
)

// Middleware wraps a handler. The tests put these middlewares around the
// handler that the contrib traces, so that the writer directly under httptrace
// is not a known server writer.
type Middleware func(http.Handler) http.Handler

// Gzip is gzhttp. It ignores a Flush when nothing is written yet.
func Gzip(next http.Handler) http.Handler { return gzhttp.GzipHandler(next) }

// UnwrapOnly gives the next handler a writer that has only Unwrap: no Flush
// and no Hijack. A [http.ResponseController] follows Unwrap to the raw writer
// and goes around the writer that httptrace gives to the application.
func UnwrapOnly(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return unwrapOnly{w} })
}

// PassThrough gives the next handler a writer that forwards Flush to the writer
// below, and also has Unwrap. Libraries like httpsnoop and otelhttp do this.
func PassThrough(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return passThrough{w} })
}

// FailedHijacker gives the next handler a writer whose Hijack always fails.
func FailedHijacker(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return failedHijacker{w} })
}

// IgnoredFlush gives the next handler a writer whose Flush does nothing.
func IgnoredFlush(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return ignoredFlush{w} })
}

// PanicFlush gives the next handler a writer whose Flush panics.
func PanicFlush(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return panicFlush{w} })
}

// SameNameRecorder gives the next handler a writer whose type is named
// ResponseRecorder, like the type of net/http/httptest, but is not that type.
// Its Flush does nothing.
func SameNameRecorder(next http.Handler) http.Handler {
	return wrap(next, func(w http.ResponseWriter) http.ResponseWriter { return ResponseRecorder{w} })
}

// EarlyHints writes the informational status 103 before it calls the next
// handler.
func EarlyHints(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		next.ServeHTTP(w, r)
	})
}

// Chain applies the middlewares from the last to the first, so that the first
// is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}

func wrap(next http.Handler, f func(http.ResponseWriter) http.ResponseWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(f(w), r)
	})
}

type unwrapOnly struct{ http.ResponseWriter }

func (w unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type passThrough struct{ http.ResponseWriter }

func (w passThrough) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w passThrough) Flush()                      { w.ResponseWriter.(http.Flusher).Flush() }

type failedHijacker struct{ http.ResponseWriter }

func (failedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack refused")
}

type ignoredFlush struct{ http.ResponseWriter }

func (ignoredFlush) Flush() {}

type panicFlush struct{ http.ResponseWriter }

func (panicFlush) Flush() { panic("no response sent") }

// ResponseRecorder has the name of the type in net/http/httptest, but not its
// package. It must not count as a known server writer.
type ResponseRecorder struct{ http.ResponseWriter }

func (ResponseRecorder) Flush() {}
