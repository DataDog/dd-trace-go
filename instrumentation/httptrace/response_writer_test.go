// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type hijackResponseWriter struct {
	header http.Header
	err    error
}

func (w *hijackResponseWriter) Header() http.Header     { return w.header }
func (*hijackResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (*hijackResponseWriter) WriteHeader(int)           {}
func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, w.err
}

func TestResponseWriterTracksFlushAndHijack(t *testing.T) {
	t.Run("flush", func(t *testing.T) {
		wrapped, monitored := wrapResponseWriter(httptest.NewRecorder())
		wrapped.(http.Flusher).Flush()
		assert.True(t, monitored.Written())
		assert.Equal(t, http.StatusOK, monitored.Status())
	})

	t.Run("hijack", func(t *testing.T) {
		wrapped, monitored := wrapResponseWriter(&hijackResponseWriter{header: make(http.Header)})
		_, _, err := wrapped.(http.Hijacker).Hijack()
		assert.NoError(t, err)
		assert.True(t, monitored.Written())
	})

	t.Run("hijack error", func(t *testing.T) {
		wantErr := errors.New("hijack failed")
		wrapped, monitored := wrapResponseWriter(&hijackResponseWriter{header: make(http.Header), err: wantErr})
		_, _, err := wrapped.(http.Hijacker).Hijack()
		assert.ErrorIs(t, err, wantErr)
		assert.False(t, monitored.Written())
	})
}

func Test_wrapResponseWriter(t *testing.T) {
	// there doesn't appear to be an easy way to test http.Pusher support via an http request
	// so we'll just confirm wrapResponseWriter preserves it
	t.Run("Pusher", func(t *testing.T) {
		var i struct {
			http.ResponseWriter
			http.Pusher
		}
		var w http.ResponseWriter = i
		_, ok := w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)

		var monitored *responseWriter
		w, monitored = wrapResponseWriter(w)
		_, ok = w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)
		written, ok := w.(interface{ Written() bool })
		assert.True(t, ok)
		assert.False(t, written.Written())

		monitored.status = http.StatusCreated
		monitored.committed = true
		ResetStatusCode(w)
		assert.Zero(t, monitored.Status())
		assert.False(t, monitored.Written())
	})

}
