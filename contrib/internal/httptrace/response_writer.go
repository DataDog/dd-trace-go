// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"net/http"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var warnLogOnce sync.Once

const warnLogMsg = `appsec: http.ResponseWriter was used after a security blocking decision was enacted.
Please check for gopkg.in/DataDog/dd-trace-go.v1/appsec/events.BlockingSecurityEvent in the error result value of instrumented functions.`

// TODO(eliott.bouhana): add a link to the appsec SDK documentation ^^^ here ^^^

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status  int
	blocked bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, 0, false}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Blocked returns whether the response has been blocked.
func (w *responseWriter) Blocked() bool {
	return w.blocked
}

// Block is supposed only once, after a response (one made by appsec code) as been sent. If it not the case, the function will do nothing.
// All subsequent calls to Write and WriteHeader will be trigger a log warning users that the response has been blocked.
func (w *responseWriter) Block() {
	if !appsec.Enabled() || w.status == 0 {
		return
	}

	w.blocked = true
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.blocked {
		warnLogOnce.Do(func() {
			log.Warn(warnLogMsg)
		})
		return len(b), nil
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.blocked {
		warnLogOnce.Do(func() {
			log.Warn(warnLogMsg)
		})
		return
	}
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
