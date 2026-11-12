// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

		w, _ = wrapResponseWriter(w)
		_, ok = w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)
	})

}

// opaqueFlush is a writer under the monitored writer whose Flush is not the
// Flush of a known server writer. If fail is true, Flush panics.
type opaqueFlush struct {
	http.ResponseWriter
	fail bool
}

func (w opaqueFlush) Flush() {
	if w.fail {
		panic("Flush did not run")
	}
}

// onlyWriter has no optional interface, so wrapResponseWriter returns the base
// *responseWriter.
type onlyWriter struct{ http.ResponseWriter }

func TestResponseWriterResetStatusCode(t *testing.T) {
	t.Run("base writer", func(t *testing.T) {
		w, rw := wrapResponseWriter(onlyWriter{httptest.NewRecorder()})
		require.Same(t, rw, w, "the base writer has no optional interface")
		w.WriteHeader(http.StatusOK)
		require.True(t, rw.Committed())
		ResetStatusCode(w)
		assert.Zero(t, rw.Status())
		assert.False(t, rw.Committed(), "no Flush was recorded")
	})
	t.Run("keeps a Flush", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w, rw := wrapResponseWriter(rec)
		w.(http.Flusher).Flush()
		ResetStatusCode(rw)
		assert.Zero(t, rw.Status())
		assert.True(t, rw.Committed(), "ResetStatusCode changes the status only")
	})
	t.Run("generated wrapper", func(t *testing.T) {
		// This is the behavior on main: only the base writer is reset.
		w, rw := wrapResponseWriter(httptest.NewRecorder())
		w.WriteHeader(http.StatusOK)
		ResetStatusCode(w)
		assert.Equal(t, http.StatusOK, rw.Status())
		assert.True(t, rw.Committed())
	})
}

func TestResponseWriterNestedMarker(t *testing.T) {
	t.Run("known", func(t *testing.T) {
		inner, iw := wrapResponseWriter(httptest.NewRecorder())
		outer, ow := wrapResponseWriter(inner)
		outer.(http.Flusher).Flush()
		assert.True(t, iw.wireCommitted())
		assert.True(t, ow.wireCommitted())
		assert.Equal(t, http.StatusOK, iw.Status())
		assert.Equal(t, http.StatusOK, ow.Status())
	})
	for _, fail := range []bool{false, true} {
		t.Run("opaque", func(t *testing.T) {
			inner, iw := wrapResponseWriter(opaqueFlush{httptest.NewRecorder(), fail})
			outer, ow := wrapResponseWriter(inner)
			func() {
				defer func() { _ = recover() }()
				outer.(http.Flusher).Flush()
			}()
			assert.False(t, iw.Committed() || ow.Committed() || iw.wireCommitted() || ow.wireCommitted())
		})
	}
}

// TestResponseWriterSize checks that the writer stays at 24 bytes on a 64-bit
// platform, so that a wrapped writer does not allocate more than before.
func TestResponseWriterSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the size depends on the word size")
	}
	assert.EqualValues(t, 24, unsafe.Sizeof(responseWriter{}))
}

func TestResponseWriterStatusClamp(t *testing.T) {
	// net/http allows only the statuses 100 to 999, but another writer can
	// allow more. A big status must not wrap around to 0.
	if strconv.IntSize < 64 {
		t.Skip("the status does not fit in an int on a 32-bit platform")
	}
	// A variable, not a constant: a constant that overflows int does not
	// compile on a 32-bit platform.
	big := int64(1) << 40
	w := newResponseWriter(nopHeaderWriter{})
	w.WriteHeader(int(big))
	assert.NotZero(t, w.Status())
	assert.True(t, w.Committed())
}

type nopHeaderWriter struct{ http.ResponseWriter }

func (nopHeaderWriter) WriteHeader(int) {}
