// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import "net/http"

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status int
}

// ResetStatusCode resets the status code of the response writer.
func ResetStatusCode(w http.ResponseWriter) {
	if rw, ok := w.(*responseWriter); ok {
		rw.status = 0
	}
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, 0}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
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
	w.status = status
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
