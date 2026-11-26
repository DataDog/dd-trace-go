// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/klauspost/compress/gzhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// These tests use only the exported API of the package. They check what a
// handler can see on the writer that BeforeHandle returns.

// snapshot is what the writer reports after the handler used it.
type snapshot struct {
	committed bool
	// hasCommitted is false if the writer has no Committed method.
	hasCommitted bool
	status       int
}

func snap(w http.ResponseWriter) snapshot {
	var s snapshot
	if c, ok := w.(interface{ Committed() bool }); ok {
		s.hasCommitted, s.committed = true, c.Committed()
	}
	if c, ok := w.(interface{ Status() int }); ok {
		s.status = c.Status()
	}
	return s
}

// startTrace wraps w the way a contrib does. It does not use the testing.T, so
// a server goroutine can call it. AppSec must be off: the tests that call it
// start with disableAppSec.
func startTrace(w http.ResponseWriter) (tw http.ResponseWriter, after func(), handled bool) {
	tw, _, after, handled = httptrace.BeforeHandle(nil, w, httptest.NewRequest(http.MethodGet, "/", nil))
	return tw, after, handled
}

// disableAppSec turns AppSec off for the test, so that BeforeHandle only traces.
func disableAppSec(t *testing.T) {
	t.Helper()
	t.Setenv("DD_APPSEC_ENABLED", "false")
}

// trace is startTrace for the test goroutine. It returns the traced writer.
func trace(t *testing.T, w http.ResponseWriter) http.ResponseWriter {
	t.Helper()
	tw, after, handled := startTrace(w)
	require.False(t, handled)
	t.Cleanup(after)
	return tw
}

// opaqueFlusher is a writer under httptrace whose Flush is not the Flush of a
// server writer.
type opaqueFlusher struct {
	http.ResponseWriter
	flush func()
}

func (w opaqueFlusher) Flush() { w.flush() }

// ResponseRecorder has the name of the type in net/http/httptest, but is not
// that type.
type ResponseRecorder struct{ http.ResponseWriter }

func (ResponseRecorder) Flush() {}

type failedHijacker struct{ http.ResponseWriter }

func (failedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack refused")
}

func TestCommitted(t *testing.T) {
	disableAppSec(t)
	t.Run("fresh", func(t *testing.T) {
		s := snap(trace(t, httptest.NewRecorder()))
		assert.True(t, s.hasCommitted)
		assert.False(t, s.committed)
		assert.Zero(t, s.status)
	})
	t.Run("write", func(t *testing.T) {
		w := trace(t, httptest.NewRecorder())
		_, _ = w.Write([]byte("body"))
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 200}, snap(w))
	})
	t.Run("write-header", func(t *testing.T) {
		w := trace(t, httptest.NewRecorder())
		w.WriteHeader(http.StatusNotFound)
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 404}, snap(w))
	})
	t.Run("known-flush", func(t *testing.T) {
		w := trace(t, httptest.NewRecorder())
		w.(http.Flusher).Flush()
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 200}, snap(w))
	})
	t.Run("known-flush-controller", func(t *testing.T) {
		w := trace(t, httptest.NewRecorder())
		require.NoError(t, http.NewResponseController(w).Flush())
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 200}, snap(w))
	})
	t.Run("known-flush-drops-write-header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := trace(t, rec)
		w.(http.Flusher).Flush()
		w.WriteHeader(http.StatusNotFound)
		assert.Equal(t, 200, snap(w).status)
		assert.Equal(t, 200, rec.Code)
	})
	t.Run("opaque-flush", func(t *testing.T) {
		flushed := false
		w := trace(t, opaqueFlusher{httptest.NewRecorder(), func() { flushed = true }})
		w.(http.Flusher).Flush()
		assert.True(t, flushed, "the Flush must reach the writer below")
		assert.Equal(t, snapshot{hasCommitted: true}, snap(w), "an opaque Flush is not a commit")
	})
	t.Run("panic-flush", func(t *testing.T) {
		w := trace(t, opaqueFlusher{httptest.NewRecorder(), func() { panic("no response sent") }})
		assert.Panics(t, w.(http.Flusher).Flush)
		assert.Equal(t, snapshot{hasCommitted: true}, snap(w))
	})
	t.Run("failed-hijack", func(t *testing.T) {
		w := trace(t, failedHijacker{httptest.NewRecorder()})
		_, _, err := w.(http.Hijacker).Hijack()
		require.Error(t, err)
		assert.Equal(t, snapshot{hasCommitted: true}, snap(w))
	})
	// A handler runs on a server goroutine: it records the values, and the
	// test goroutine asserts them.
	hijack := func(t *testing.T, hijackFn func(http.ResponseWriter) (net.Conn, error)) (s snapshot, err error) {
		var after func()
		var handled bool
		get(t, http1, func(w http.ResponseWriter, _ *http.Request) {
			var tw http.ResponseWriter
			tw, after, handled = startTrace(w)
			conn, herr := hijackFn(tw)
			err = herr
			s = snap(tw)
			if conn != nil {
				_ = conn.Close()
			}
		})
		after()
		require.False(t, handled)
		return s, err
	}
	t.Run("hijack", func(t *testing.T) {
		s, err := hijack(t, func(w http.ResponseWriter) (net.Conn, error) {
			conn, _, err := w.(http.Hijacker).Hijack()
			return conn, err
		})
		require.NoError(t, err)
		assert.Equal(t, snapshot{committed: true, hasCommitted: true}, s, "a Hijack is a commit, with no status")
	})
	t.Run("hijack-controller", func(t *testing.T) {
		s, err := hijack(t, func(w http.ResponseWriter) (net.Conn, error) {
			conn, _, err := http.NewResponseController(w).Hijack()
			return conn, err
		})
		require.NoError(t, err)
		assert.Equal(t, snapshot{committed: true, hasCommitted: true}, s)
	})
	t.Run("nested-known", func(t *testing.T) {
		inner := trace(t, httptest.NewRecorder())
		outer := trace(t, inner)
		outer.(http.Flusher).Flush()
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 200}, snap(outer))
		assert.Equal(t, snapshot{committed: true, hasCommitted: true, status: 200}, snap(inner))
	})
	t.Run("nested-opaque", func(t *testing.T) {
		inner := trace(t, opaqueFlusher{httptest.NewRecorder(), func() {}})
		outer := trace(t, inner)
		outer.(http.Flusher).Flush()
		assert.Equal(t, snapshot{hasCommitted: true}, snap(outer))
		assert.Equal(t, snapshot{hasCommitted: true}, snap(inner))
	})
	t.Run("nested-failed", func(t *testing.T) {
		inner := trace(t, opaqueFlusher{httptest.NewRecorder(), func() { panic("no response sent") }})
		outer := trace(t, inner)
		assert.Panics(t, outer.(http.Flusher).Flush)
		assert.Equal(t, snapshot{hasCommitted: true}, snap(outer))
		assert.Equal(t, snapshot{hasCommitted: true}, snap(inner))
	})
}

type proto string

const (
	http1  proto = "http1"
	http2s proto = "http2"
	h2cs   proto = "h2c"
)

// startTestServer starts a real server that runs h, and returns it with a
// client for it. The server closes at the end of the test.
func startTestServer(t *testing.T, p proto, h http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	if p == h2cs {
		h = h2c.NewHandler(h, &http2.Server{})
	}
	srv := httptest.NewUnstartedServer(h)
	if p == http2s {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Timeout = 5 * time.Second
	if p == h2cs {
		protocols := new(http.Protocols)
		protocols.SetUnencryptedHTTP2(true)
		client.Transport = &http.Transport{Protocols: protocols}
	}
	return srv, client
}

// get sends a GET request to a real server that runs h, and waits for h.
func get(t *testing.T, p proto, h http.HandlerFunc) {
	t.Helper()
	done := make(chan struct{})
	srv, client := startTestServer(t, p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h(w, r)
	}))
	// A Hijack closes the connection before a response: an error is expected.
	if resp, err := client.Get(srv.URL); err == nil {
		resp.Body.Close()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return")
	}
}

// TestKnownServerWriter checks which writers under httptrace count as a server
// writer: for those, a Flush is a commit. The types of the server writers are
// not exported and change with the Go version (for example, the HTTP/2 writer
// is a pointer in Go 1.26 and a value in Go 1.27). If a Go release renames one,
// this test fails and shows it, and the behavior stays the same as without the
// fix until the list is updated.
func TestKnownServerWriter(t *testing.T) {
	disableAppSec(t)
	// flushOnServer runs Flush on the writer that a real server gives, after
	// wrapUnder wrapped it. It returns Committed() of the traced writer.
	flushOnServer := func(t *testing.T, p proto, wrapUnder func(http.ResponseWriter) http.ResponseWriter) bool {
		t.Helper()
		var committed, handled bool
		done := make(chan struct{})
		srv, client := startTestServer(t, p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer close(done)
			var tw http.ResponseWriter
			var after func()
			tw, after, handled = startTrace(wrapUnder(w))
			defer after()
			tw.(http.Flusher).Flush()
			committed = snap(tw).committed
		}))
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		resp.Body.Close()
		<-done
		require.False(t, handled)
		return committed
	}
	identity := func(w http.ResponseWriter) http.ResponseWriter { return w }

	t.Run("known", func(t *testing.T) {
		assert.True(t, flushOnServer(t, http1, identity), "net/http HTTP/1")
		assert.True(t, flushOnServer(t, http2s, identity), "net/http HTTP/2")
		assert.True(t, flushOnServer(t, h2cs, identity), "golang.org/x/net/http2 (h2c)")
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"NewRecorder":   httptest.NewRecorder(),
			"zero recorder": new(httptest.ResponseRecorder),
		} {
			w := trace(t, rec)
			w.(http.Flusher).Flush()
			assert.True(t, snap(w).committed, name)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		assert.False(t, flushOnServer(t, http1, func(w http.ResponseWriter) http.ResponseWriter {
			return passThrough{w}
		}), "a pass-through writer")
		assert.False(t, flushOnServer(t, http1, func(w http.ResponseWriter) http.ResponseWriter {
			return ResponseRecorder{w}
		}), "a type that is named ResponseRecorder")
		assert.False(t, flushOnServer(t, http1, func(w http.ResponseWriter) http.ResponseWriter {
			return opaqueFlusher{w, w.(http.Flusher).Flush}
		}), "a value type with an unknown name")
		assert.False(t, flushOnServer(t, http2s, func(w http.ResponseWriter) http.ResponseWriter {
			return passThrough{w}
		}), "a pass-through writer on HTTP/2")
	})

	// gzhttp ignores a Flush when nothing is written and the client accepts gzip.
	t.Run("gzhttp", func(t *testing.T) {
		var committed, handled bool
		done := make(chan struct{})
		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer close(done)
			var tw http.ResponseWriter
			var after func()
			tw, after, handled = startTrace(w)
			defer after()
			tw.(http.Flusher).Flush()
			committed = snap(tw).committed
		})
		srv, client := startTestServer(t, http1, gzhttp.GzipHandler(inner))
		resp, err := client.Get(srv.URL) // the client sends Accept-Encoding: gzip
		require.NoError(t, err)
		resp.Body.Close()
		<-done
		require.False(t, handled)
		assert.False(t, committed)
	})
}

type passThrough struct{ http.ResponseWriter }

func (w passThrough) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w passThrough) Flush()                      { w.ResponseWriter.(http.Flusher).Flush() }

// TestWrapperInterfaces checks the 16 sets of optional interfaces: the traced
// writer must keep the same subset of Flusher, Pusher, CloseNotifier and
// Hijacker. It must not add FlushError or ReaderFrom. The exported methods
// must be the known ones: the marker that nested writers use is not exported.
func TestWrapperInterfaces(t *testing.T) {
	known := []string{"Committed", "Header", "Status", "Unwrap", "Write", "WriteHeader"}
	for mask := range 16 {
		t.Run(fmt.Sprintf("set-%02d", mask), func(t *testing.T) {
			tw := trace(t, withInterfaces(mask, httptest.NewRecorder()))

			_, f1 := tw.(http.Flusher)
			_, p1 := tw.(http.Pusher)
			_, n1 := tw.(http.CloseNotifier)
			_, h1 := tw.(http.Hijacker)
			assert.Equal(t, mask&1 != 0, f1, "Flusher")
			assert.Equal(t, mask&2 != 0, p1, "Pusher")
			assert.Equal(t, mask&4 != 0, n1, "CloseNotifier")
			assert.Equal(t, mask&8 != 0, h1, "Hijacker")
			_, ok := tw.(interface{ FlushError() error })
			assert.False(t, ok, "FlushError")
			_, ok = tw.(io.ReaderFrom)
			assert.False(t, ok, "ReaderFrom")

			var methods []string
			typ := reflect.TypeOf(tw)
			for m := range typ.Methods() {
				methods = append(methods, m.Name)
			}
			sort.Strings(methods)
			extra := []string{"Flush", "Push", "CloseNotify", "Hijack"}
			for _, m := range methods {
				assert.True(t, slices.Contains(known, m) || slices.Contains(extra, m), "unexpected exported method %s", m)
			}
		})
	}
}

type caps struct{ http.ResponseWriter }

func (caps) Flush()                               {}
func (caps) Push(string, *http.PushOptions) error { return nil }
func (caps) CloseNotify() <-chan bool             { return nil }
func (caps) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("not supported")
}

// withInterfaces returns a writer that has the optional interfaces of mask (bit
// 0: Flusher, 1: Pusher, 2: CloseNotifier, 3: Hijacker).
func withInterfaces(mask int, base http.ResponseWriter) http.ResponseWriter {
	c := caps{base}
	type b = struct{ http.ResponseWriter }
	switch mask {
	case 0:
		return b{base}
	case 1:
		return struct {
			b
			http.Flusher
		}{b{base}, c}
	case 2:
		return struct {
			b
			http.Pusher
		}{b{base}, c}
	case 3:
		return struct {
			b
			http.Flusher
			http.Pusher
		}{b{base}, c, c}
	case 4:
		return struct {
			b
			http.CloseNotifier
		}{b{base}, c}
	case 5:
		return struct {
			b
			http.Flusher
			http.CloseNotifier
		}{b{base}, c, c}
	case 6:
		return struct {
			b
			http.Pusher
			http.CloseNotifier
		}{b{base}, c, c}
	case 7:
		return struct {
			b
			http.Flusher
			http.Pusher
			http.CloseNotifier
		}{b{base}, c, c, c}
	case 8:
		return struct {
			b
			http.Hijacker
		}{b{base}, c}
	case 9:
		return struct {
			b
			http.Flusher
			http.Hijacker
		}{b{base}, c, c}
	case 10:
		return struct {
			b
			http.Pusher
			http.Hijacker
		}{b{base}, c, c}
	case 11:
		return struct {
			b
			http.Flusher
			http.Pusher
			http.Hijacker
		}{b{base}, c, c, c}
	case 12:
		return struct {
			b
			http.CloseNotifier
			http.Hijacker
		}{b{base}, c, c}
	case 13:
		return struct {
			b
			http.Flusher
			http.CloseNotifier
			http.Hijacker
		}{b{base}, c, c, c}
	case 14:
		return struct {
			b
			http.Pusher
			http.CloseNotifier
			http.Hijacker
		}{b{base}, c, c, c}
	default:
		return struct {
			b
			http.Flusher
			http.Pusher
			http.CloseNotifier
			http.Hijacker
		}{b{base}, c, c, c, c}
	}
}
