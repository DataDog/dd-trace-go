// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package httpsec

import (
	"net/http"
	"testing"
)

type writtenResponseWriter struct {
	header  http.Header
	status  int
	written bool
}

func (w *writtenResponseWriter) Header() http.Header {
	return w.header
}

func (w *writtenResponseWriter) Write(b []byte) (int, error) {
	w.written = true
	return len(b), nil
}

func (w *writtenResponseWriter) WriteHeader(status int) {
	w.status = status
	w.written = true
}

func (w *writtenResponseWriter) Status() int {
	return w.status
}

func (w *writtenResponseWriter) Written() bool {
	return w.written
}

func TestResponseStartedPrefersWritten(t *testing.T) {
	w := &writtenResponseWriter{header: make(http.Header), status: http.StatusOK}
	if responseStarted(w) {
		t.Fatal("pre-seeded status must not imply that response headers were sent")
	}

	w.WriteHeader(http.StatusOK)
	if !responseStarted(w) {
		t.Fatal("committed response was not detected")
	}
}
