// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"bufio"
	"math"
	"net"
	"net/http"
	"reflect"
)

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request. It also records when the
// response is started without a status: a Flush that is sure to be on the
// wire, or a successful Hijack.
type responseWriter struct {
	http.ResponseWriter
	// status is the status that was sent, or 0. It is an int32 to keep the
	// struct at 24 bytes: net/http only allows the statuses 100 to 999.
	status int32
	// committed is true after a Flush that is known to be on the wire (see
	// [flushIsOnWire]) or after a successful Hijack.
	committed bool
}

// ResetStatusCode resets the status code of the response writer. It works only
// on the base writer, which has no Flush and no Hijack. It changes the status
// only: a Flush or a Hijack that was recorded stays recorded.
func ResetStatusCode(w http.ResponseWriter) {
	if rw, ok := w.(*responseWriter); ok {
		rw.status = 0
	}
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

// Status returns the status code that was monitored. After a Flush that is
// known to be on the wire, and with no status written before, it is 200: this
// is the status that the client gets.
func (w *responseWriter) Status() int {
	return int(w.status)
}

// Committed tells if the response is started, so that a block response cannot
// replace it. It is true after a Write or a WriteHeader, after a Flush that is
// known to be on the wire, and after a successful Hijack.
func (w *responseWriter) Committed() bool {
	return w.status != 0 || w.committed
}

// wireCommitted tells if a Flush or a Hijack was recorded. The method is not
// exported, so that applications cannot call it or test for it. A writer of
// this type that is directly above another one uses it to know that the Flush
// reached the wire.
func (w *responseWriter) wireCommitted() bool {
	return w.committed
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	// net/http only allows the statuses 100 to 999, so the clamp is for
	// writers that allow anything: a wrap-around must not give status 0.
	w.status = int32(min(max(status, math.MinInt32), math.MaxInt32))
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// flush calls Flush on the writer below. If that Flush is known to send the
// status line and the headers, the response is started and the status is 200,
// unless a status was written before. In all other cases nothing changes,
// because the writer below can ignore the Flush: gzhttp does this when nothing
// is written yet. A Flush that panics records nothing.
func (w *responseWriter) flush() {
	w.ResponseWriter.(http.Flusher).Flush()
	if w.committed || !flushIsOnWire(w.ResponseWriter) {
		return
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.committed = true
}

// hijack calls Hijack on the writer below. After a successful Hijack, the
// server does not send anything more on the connection, so a block response
// cannot reach the client.
func (w *responseWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.committed = true
	}
	return c, rw, err
}

// flusher and hijacker give the writers that wrapResponseWriter generates a
// Flush and a Hijack that go through the monitored writer. They hold one
// pointer each, so they do not allocate when they become an interface value.
type flusher struct{ w *responseWriter }

func (f flusher) Flush() { f.w.flush() }

type hijacker struct{ w *responseWriter }

func (h hijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return h.w.hijack() }

// flushIsOnWire tells if a Flush on next sends the status line and the headers
// to the client. next is the writer directly below the monitored writer. It
// is sure in two cases only:
//   - next is another monitored writer, and that writer recorded the Flush;
//   - next is a known server writer (see [knownServerWriter]).
//
// An opaque writer can ignore the Flush, so nothing can know if the bytes
// were sent.
func flushIsOnWire(next http.ResponseWriter) bool {
	if n, ok := next.(interface{ wireCommitted() bool }); ok {
		return n.wireCommitted()
	}
	return knownServerWriter(next)
}

// knownServerWriter tells if w is the response writer of a server that always
// sends the status line and the headers on Flush, or (for the recorder) locks
// the status. They are:
//   - net/http, HTTP/1 and HTTP/2;
//   - golang.org/x/net/http2 (h2c);
//   - net/http/httptest.ResponseRecorder.
//
// The types are not exported and the HTTP/2 type depends on the Go version: a
// pointer in Go 1.26 and a value in Go 1.27. So the check compares the package
// path and the name of the type with reflect, for a pointer and for a value.
// If a Go release renames a type, the answer is false and the behavior is the
// same as without the fix. TestKnownServerWriter fails, so that we see it.
func knownServerWriter(w http.ResponseWriter) bool {
	t := reflect.TypeOf(w)
	if t == nil {
		return false
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.PkgPath() {
	case "net/http":
		switch t.Name() {
		case "response", "http2responseWriter", "http2ResponseWriter":
			return true
		}
	case "golang.org/x/net/http2":
		return t.Name() == "responseWriter"
	case "net/http/httptest":
		return t.Name() == "ResponseRecorder"
	}
	return false
}
