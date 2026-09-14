// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"bufio"
	"net"
	"net/http"
)

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status    int
	committed bool
}

// ResetStatusCode resets the monitored status and committed state so an
// integration can replace a staged response.
func ResetStatusCode(w http.ResponseWriter) {
	for w != nil {
		if rw, ok := w.(interface{ resetStatusCode() }); ok {
			rw.resetStatusCode()
			return
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = unwrapper.Unwrap()
	}
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Written reports whether the response headers were sent.
func (w *responseWriter) Written() bool {
	return w.committed
}

func (w *responseWriter) resetStatusCode() {
	w.status = 0
	w.committed = false
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
	if w.committed {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	w.committed = true
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type responseWriterFlusher struct {
	http.Flusher
	responseWriter *responseWriter
}

func (w responseWriterFlusher) Flush() {
	if !w.responseWriter.Written() {
		w.responseWriter.WriteHeader(http.StatusOK)
	}
	w.Flusher.Flush()
}

type responseWriterHijacker struct {
	http.Hijacker
	responseWriter *responseWriter
}

func (w responseWriterHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.Hijacker.Hijack()
	if err == nil {
		w.responseWriter.committed = true
	}
	return conn, rw, err
}
