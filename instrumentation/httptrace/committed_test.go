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

// trace wraps w the way a contrib does. It returns the traced writer.
func trace(t *testing.T, w http.ResponseWriter) http.ResponseWriter {
	t.Helper()
	t.Setenv("DD_APPSEC_ENABLED", "false")
	tw, _, after, handled := httptrace.BeforeHandle(nil, w, httptest.NewRequest(http.MethodGet, "/", nil))
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

type unwrapOnly struct{ http.ResponseWriter }

func (w unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type failedHijacker struct{ http.ResponseWriter }

func (failedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack refused")
}

func TestCommitted(t *testing.T) {
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
	t.Run("hijack", func(t *testing.T) {
		var s snapshot
		get(t, http1, func(w http.ResponseWriter, _ *http.Request) {
			tw := trace(t, w)
			conn, _, err := tw.(http.Hijacker).Hijack()
			require.NoError(t, err)
			s = snap(tw)
			_ = conn.Close()
		})
		assert.Equal(t, snapshot{committed: true, hasCommitted: true}, s, "a Hijack is a commit, with no status")
	})
	t.Run("hijack-controller", func(t *testing.T) {
		var s snapshot
		get(t, http1, func(w http.ResponseWriter, _ *http.Request) {
			tw := trace(t, w)
			conn, _, err := http.NewResponseController(tw).Hijack()
			require.NoError(t, err)
			s = snap(tw)
			_ = conn.Close()
		})
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

// get sends a GET request to a real server that runs h, and waits for h.
func get(t *testing.T, p proto, h http.HandlerFunc) {
	t.Helper()
	done := make(chan struct{})
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h(w, r)
	})
	if p == h2cs {
		handler = h2c.NewHandler(handler, &http2.Server{})
	}
	srv := httptest.NewUnstartedServer(handler)
	if p == http2s {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 5 * time.Second
	if p == h2cs {
		protocols := new(http.Protocols)
		protocols.SetUnencryptedHTTP2(true)
		client.Transport = &http.Transport{Protocols: protocols}
	}
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
	flushOnServer := func(t *testing.T, p proto, wrapUnder func(http.ResponseWriter) http.ResponseWriter, accept string) bool {
		t.Helper()
		var committed bool
		done := make(chan struct{})
		handler := func(w http.ResponseWriter, r *http.Request) {
			tw := trace(t, wrapUnder(w))
			tw.(http.Flusher).Flush()
			committed = snap(tw).committed
			close(done)
		}
		var h http.Handler = http.HandlerFunc(handler)
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
		defer srv.Close()
		client := srv.Client()
		client.Timeout = 5 * time.Second
		if p == h2cs {
			protocols := new(http.Protocols)
			protocols.SetUnencryptedHTTP2(true)
			client.Transport = &http.Transport{Protocols: protocols}
		}
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		<-done
		return committed
	}
	identity := func(w http.ResponseWriter) http.ResponseWriter { return w }

	t.Run("known", func(t *testing.T) {
		assert.True(t, flushOnServer(t, http1, identity, ""), "net/http HTTP/1")
		assert.True(t, flushOnServer(t, http2s, identity, ""), "net/http HTTP/2")
		assert.True(t, flushOnServer(t, h2cs, identity, ""), "golang.org/x/net/http2 (h2c)")
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
		}, ""), "a pass-through writer")
		assert.False(t, flushOnServer(t, http1, func(w http.ResponseWriter) http.ResponseWriter {
			return ResponseRecorder{w}
		}, ""), "a type that is named ResponseRecorder")
		assert.False(t, flushOnServer(t, http1, func(w http.ResponseWriter) http.ResponseWriter {
			return opaqueFlusher{w, w.(http.Flusher).Flush}
		}, ""), "a value type with an unknown name")
		assert.False(t, flushOnServer(t, http2s, func(w http.ResponseWriter) http.ResponseWriter {
			return passThrough{w}
		}, ""), "a pass-through writer on HTTP/2")
	})

	// gzhttp ignores a Flush when nothing is written and the client accepts gzip.
	t.Run("gzhttp", func(t *testing.T) {
		var committed bool
		done := make(chan struct{})
		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			tw := trace(t, w)
			tw.(http.Flusher).Flush()
			committed = snap(tw).committed
			close(done)
		})
		srv := httptest.NewServer(gzhttp.GzipHandler(inner))
		defer srv.Close()
		resp, err := srv.Client().Get(srv.URL) // the client sends Accept-Encoding: gzip
		require.NoError(t, err)
		resp.Body.Close()
		<-done
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
