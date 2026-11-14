// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

const (
	appBody   = "application-body"
	rawAnswer = "HTTP/1.1 200 OK\r\nContent-Length: 16\r\n\r\n" + appBody
)

// syncBuffer is a log destination that the server goroutines can write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// wireResult is what the client received, and what AppSec reported.
type wireResult struct {
	status   int
	body     string
	location string
	log      string
	events   []string
	routes   []string
	metrics  map[string]float64
	ran      bool
}

func (r wireResult) requestBlocked() float64 {
	return count(r.metrics, "waf.requests", "request_blocked:true")
}
func (r wireResult) blockFailure() float64 {
	return count(r.metrics, "waf.requests", "block_failure:true")
}

// wireServer is the server of one request: the router, and a function that
// decorates the writer that net/http gives (nil: none).
type wireServer struct {
	router http.Handler
	wrap   func(http.ResponseWriter, *http.Request) (http.ResponseWriter, *http.Request)
	http2  bool
}

// do sends one GET (or the method) request to the server and waits for the
// handler to return.
func (s wireServer) do(t *testing.T, rec *metricsRecorder, capture *opCapture, method, target string) wireResult {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()
	capture.reset()
	before := rec.snapshot()

	done := make(chan struct{})
	var logs syncBuffer
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		if s.wrap != nil {
			w, r = s.wrap(w, r)
		}
		s.router.ServeHTTP(w, r)
	}))
	srv.Config.ErrorLog = log.New(&logs, "", 0)
	srv.EnableHTTP2 = s.http2
	if s.http2 {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	defer srv.Close()

	client := srv.Client()
	client.Timeout = 10 * time.Second // a server that hangs must fail the test
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(method, srv.URL+target, nil)
	require.NoError(t, err)
	res, err := client.Do(req)
	require.NoError(t, err)
	if s.http2 {
		require.Equal(t, 2, res.ProtoMajor, "the request must use HTTP/2")
	}
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	body = gunzipIfNeeded(t, body)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return")
	}

	out := wireResult{status: res.StatusCode, body: string(body), location: res.Header.Get("Location"), log: logs.String(),
		routes: capture.routes(), metrics: rec.since(before)}
	seen := map[string]bool{}
	for _, span := range mt.FinishedSpans() {
		for _, id := range eventRules(span.Tag("_dd.appsec.json")) {
			seen[id] = true
		}
	}
	for id := range seen {
		out.events = append(out.events, id)
	}
	slices.Sort(out.events)
	return out
}

// gunzipIfNeeded returns the decompressed body when it is a gzip stream. A
// compressing middleware can send a gzip body after a flush has sent the
// headers without Content-Encoding: the client does not decompress it, and the
// compressed bytes depend on the Go version. The tests check the documented
// behavior, not the compressed bytes.
func gunzipIfNeeded(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	return plain
}

// Response writers that a test puts between net/http and the router.

// flushErrorWriter has FlushError, and neither Flush, Hijack nor Unwrap.
type flushErrorWriter struct {
	http.ResponseWriter
	err error // the result of FlushError, nil to flush
}

func (w flushErrorWriter) FlushError() error {
	if w.err != nil {
		return w.err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// cyclicWriter returns itself from Unwrap(). After many calls it returns nil, so
// that a loop over Unwrap() that does not stop (it exists in code that the
// tracker does not control) ends and fails the test, instead of hanging it.
type cyclicWriter struct {
	http.ResponseWriter
	calls *atomic.Int64
}

func (w *cyclicWriter) Unwrap() http.ResponseWriter {
	if w.calls.Add(1) > 10000 {
		return nil
	}
	return w
}

// unwrapOnly hides the optional interfaces and keeps Unwrap(), so that
// http.ResponseController reaches the connection (a Hijacker) only through it.
type unwrapOnly struct{ http.ResponseWriter }

func (w unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ignoreFlushWriter is like a compressing writer: it ignores a Flush until a first
// write, and it has Unwrap().
type ignoreFlushWriter struct {
	http.ResponseWriter
	written bool
}

func (w *ignoreFlushWriter) Write(b []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(b)
}

func (w *ignoreFlushWriter) Flush() {
	if w.written {
		w.ResponseWriter.(http.Flusher).Flush()
	}
}

func (w *ignoreFlushWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// bufferedFlushErrorWriter has FlushError, and it keeps the flush: it returns nil
// and sends nothing.
type bufferedFlushErrorWriter struct{ http.ResponseWriter }

func (bufferedFlushErrorWriter) FlushError() error { return nil }

// opaqueWriter has no Unwrap() and no optional interface.
type opaqueWriter struct{ http.ResponseWriter }

func wrapWriter(f func(http.ResponseWriter) http.ResponseWriter) func(http.ResponseWriter, *http.Request) (http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request) { return f(w), r }
}

// Endpoint handler modes.

type wireMode struct {
	name   string
	sent   bool   // the response is sent when the handler returns
	body   string // the bytes of a sent response
	raw    bool   // the response is the raw bytes of a hijacked connection
	http2  bool
	nested bool // uses the router with 2 tracers (nested-*)
	// asMain: the flush reaches a writer that the tracker does not know, so the
	// state is the one without the tracker: the block body follows the flushed
	// response (a documented limit, the same result as before this change).
	asMain bool
	wrap   func(http.ResponseWriter, *http.Request) (http.ResponseWriter, *http.Request)
	do     func(w http.ResponseWriter)
}

func wireModes() []wireMode {
	rf := func(r io.Reader) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) { _, _ = w.(io.ReaderFrom).ReadFrom(r) }
	}
	tee := func(r io.Reader) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.(middleware.WrapResponseWriter).Tee(io.Discard)
			_, _ = w.(io.ReaderFrom).ReadFrom(r)
		}
	}
	flush := func(w http.ResponseWriter) { w.(http.Flusher).Flush() }
	rcFlush := func(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush() }
	hijack := func(w http.ResponseWriter) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		_, _ = io.WriteString(conn, rawAnswer)
		_ = conn.Close()
	}
	rcHijack := func(w http.ResponseWriter) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		_, _ = io.WriteString(conn, rawAnswer)
		_ = conn.Close()
	}
	return []wireMode{
		{name: "write", sent: true, body: appBody, do: func(w http.ResponseWriter) { w.WriteHeader(200); _, _ = io.WriteString(w, appBody) }},
		{name: "readfrom", sent: true, body: appBody, do: rf(strings.NewReader(appBody))},
		{name: "readfrom-empty", sent: true, do: rf(strings.NewReader(""))},
		{name: "flush-write", sent: true, body: appBody, do: func(w http.ResponseWriter) { flush(w); _, _ = io.WriteString(w, appBody) }},
		{name: "flush-only", sent: true, do: flush},
		{name: "rc-flush-only", sent: true, do: rcFlush},
		{name: "flusherror-only", sent: true, asMain: true, do: rcFlush,
			wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter { return flushErrorWriter{ResponseWriter: w} })},
		{name: "hijack", sent: true, body: appBody, raw: true, do: hijack},
		{name: "rc-hijack", sent: true, body: appBody, raw: true, do: rcHijack},
		{name: "h2-flush-only", sent: true, http2: true, do: flush},
		{name: "nested-flush", sent: true, nested: true, do: func(http.ResponseWriter) {}},
		{name: "none", do: func(http.ResponseWriter) {}},
		{name: "readfrom-empty-tee", do: tee(strings.NewReader(""))},
		{name: "readfrom-err-tee", do: tee(errReader{})},
		{name: "h2-none", http2: true, do: func(http.ResponseWriter) {}},
		{name: "nested-none", nested: true, do: func(http.ResponseWriter) {}},
	}
}

// wireSet is a set of rules.
type wireSet struct {
	name     string
	rules    []string
	event    string // the rule id that the event must have
	redirect bool
}

var (
	wireL  = wireSet{"L", []string{rulePathBlock}, "path-block", false}
	wireLr = wireSet{"Lr", []string{rulePathRedirect}, "path-redirect", true}
	wireH  = wireSet{"H", []string{ruleHeaderBlock}, "header-block", false}
	wireM  = wireSet{"M", []string{ruleMixedBlock}, "mixed-block", false}
	wireMr = wireSet{"Mr", []string{ruleMixedRedirect}, "mixed-redirect", true}
	wireS  = wireSet{"S", []string{ruleSamePath, ruleSameHeader}, "same-path", false}
)

// wireEndpoint sets the response header that the response rules match, then runs the mode.
func wireEndpoint(m wireMode, ran *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		*ran = true
		w.Header().Set("X-Response-Stage", "ready")
		m.do(w)
	}
}

// buildWire returns the router of the mode. late: the lookup cannot match (a
// trailing slash that StripSlashes removes after the tracer), so the late run
// gives the path parameters. Otherwise the lookup gives them before the handler.
func buildWire(first func(http.Handler) http.Handler, m wireMode, late bool, h http.HandlerFunc) (http.Handler, string) {
	r := chi.NewRouter()
	switch {
	case m.nested:
		r.Use(first, Middleware())
		if strings.HasSuffix(m.name, "flush") {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.(http.Flusher).Flush()
					next.ServeHTTP(w, req)
				})
			})
		}
		child := chi.NewRouter()
		child.Use(Middleware(), middleware.StripSlashes)
		child.Get("/items/{id}", h)
		r.Mount("/api", child)
		return r, "/api/items/attack/"
	case late:
		r.Use(first, Middleware(), middleware.StripSlashes)
		r.Get("/items/{id}", h)
		return r, "/items/attack/"
	default:
		r.Use(first, Middleware())
		r.Get("/items/{id}", h)
		return r, "/items/attack"
	}
}

// requireWire checks the response and the report of a request, for a mode in
// which the handler ran. The checks on the wire and on the events are separate
// subtests.
func requireWire(t *testing.T, set wireSet, m wireMode, res wireResult) {
	t.Helper()
	t.Run("wire", func(t *testing.T) {
		if m.asMain {
			assert.Equal(t, 200, res.status)
			assert.Contains(t, res.body, `{"errors":`, "a documented limit: the same as before this change")
			assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
			return
		}
		assert.Empty(t, res.log, "no server log")
		switch {
		case m.sent && m.raw:
			assert.Equal(t, 200, res.status)
			assert.Equal(t, appBody, res.body)
		case m.sent:
			assert.Equal(t, 200, res.status)
			assert.Equal(t, m.body, res.body, "the bytes of the application, and no more")
		case set.redirect:
			assert.Equal(t, 303, res.status)
			assert.Equal(t, "/blocked", res.location)
		default:
			assert.Equal(t, 403, res.status)
			assert.Contains(t, res.body, `{"errors":`)
		}
		switch {
		case set.redirect:
			// A redirect is never a block outcome.
			assert.Zero(t, res.requestBlocked())
			assert.Zero(t, res.blockFailure())
		case m.sent:
			assert.Zero(t, res.requestBlocked())
			assert.EqualValues(t, 1, res.blockFailure())
		default:
			assert.EqualValues(t, 1, res.requestBlocked())
			assert.Zero(t, res.blockFailure())
		}
	})
	t.Run("events", func(t *testing.T) {
		assert.Contains(t, res.events, set.event)
	})
}

// TestAppSecWire sends real requests to a real server with the real WAF. A
// late block or redirect replaces the response only if it is not sent yet. It
// never changes the bytes of a sent response.
func TestAppSecWire(t *testing.T) {
	type run struct {
		set   wireSet
		late  bool // router WL (late run) or WE (lookup)
		modes []string
	}
	four := []string{"write", "flush-only", "hijack", "none"}
	all := make([]string, 0, len(wireModes()))
	for _, m := range wireModes() {
		all = append(all, m.name)
	}
	runs := []run{
		{wireL, true, all}, {wireH, true, all},
		{wireLr, true, four}, {wireM, true, four}, {wireMr, true, four}, {wireS, true, four},
		{wireM, false, four}, {wireMr, false, four},
	}
	for _, r := range runs {
		router := "WE"
		if r.late {
			router = "WL"
		}
		t.Run(router+"/"+r.set.name, func(t *testing.T) {
			rec := startAppSecRules(t, r.set.rules...)
			capture := newOpCapture()
			byName := map[string]wireMode{}
			for _, m := range wireModes() {
				byName[m.name] = m
			}
			for _, name := range r.modes {
				m := byName[name]
				t.Run(m.name, func(t *testing.T) {
					var ran bool
					handler, target := buildWire(capture.middleware, m, r.late, wireEndpoint(m, &ran))
					res := wireServer{router: handler, wrap: m.wrap, http2: m.http2}.do(t, rec, capture, http.MethodGet, target)
					require.True(t, ran)
					requireWire(t, r.set, m, res)
					t.Run("route", func(t *testing.T) {
						wantRoute := "/items/{id}"
						if m.nested {
							wantRoute = "/api/items/{id}"
						}
						require.NotEmpty(t, res.routes)
						assert.Equal(t, wantRoute, res.routes[0], "the AppSec route is the route that chi selected")
					})
				})
			}
		})
	}
}

// wireOnce runs one request with the given rules, handler action and router.
func wireOnce(t *testing.T, rules []string, server func(capture *opCapture, ran *bool) (wireServer, string)) wireResult {
	t.Helper()
	rec := startAppSecRules(t, rules...)
	capture := newOpCapture()
	var ran bool
	s, target := server(capture, &ran)
	res := s.do(t, rec, capture, http.MethodGet, target)
	res.ran = ran
	assert.Empty(t, res.log, "no server log")
	return res
}

func readyHandler(ran *bool, do func(w http.ResponseWriter)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		*ran = true
		w.Header().Set("X-Response-Stage", "ready")
		if do != nil {
			do(w)
		}
	}
}

func simpleRouter(first func(http.Handler) http.Handler, late bool, h http.HandlerFunc) (http.Handler, string) {
	return buildWire(first, wireMode{}, late, h)
}

func TestAppSecWireSpecial(t *testing.T) {
	t.Run("E: early block", func(t *testing.T) {
		res := wireOnce(t, []string{rulePathBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			r, target := simpleRouter(c.middleware, false, readyHandler(ran, nil))
			return wireServer{router: r}, target
		})
		t.Run("wire", func(t *testing.T) {
			assert.False(t, res.ran, "blocked before the handler")
			assert.Equal(t, 403, res.status)
			assert.Contains(t, res.body, `{"errors":`)
			assert.EqualValues(t, 1, res.requestBlocked())
			assert.Empty(t, res.log)
		})
		t.Run("events", func(t *testing.T) { assert.Equal(t, []string{"path-block"}, res.events) })
		t.Run("route", func(t *testing.T) { assert.Equal(t, []string{"/items/{id}"}, res.routes) })
	})

	// A chain that can hijack only through Unwrap() and a chi writer that cannot:
	// the tracker cannot see a controller hijack, so it is not complete and the
	// WAF gets no newly discovered data.
	hijackThroughUnwrap := wireServer{wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter { return unwrapOnly{w} })}
	for _, c := range []struct {
		name  string
		rules []string
		event []string
	}{
		{"complete-hijack", []string{ruleMixedBlock}, nil},
		{"complete-hijack-L", []string{rulePathBlock}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := wireOnce(t, c.rules, func(cap *opCapture, ran *bool) (wireServer, string) {
				r, target := simpleRouter(cap.middleware, false, readyHandler(ran, func(w http.ResponseWriter) {
					conn, _, err := http.NewResponseController(w).Hijack()
					require.NoError(t, err)
					_, _ = io.WriteString(conn, rawAnswer)
					_ = conn.Close()
				}))
				s := hijackThroughUnwrap
				s.router = r
				return s, target
			})
			t.Run("wire", func(t *testing.T) {
				assert.True(t, res.ran, "no block before the handler")
				assert.Equal(t, 200, res.status)
				assert.Equal(t, appBody, res.body)
				assert.Empty(t, res.log)
				assert.Zero(t, res.requestBlocked())
				assert.Zero(t, res.blockFailure())
			})
			t.Run("events", func(t *testing.T) { assert.Equal(t, c.event, res.events) })
			t.Run("route", func(t *testing.T) { assert.Equal(t, []string{"/items/{id}"}, res.routes, "the route is set") })
		})
	}

	t.Run("cycle", func(t *testing.T) {
		res := wireOnce(t, []string{rulePathBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			r, target := simpleRouter(c.middleware, false, readyHandler(ran, func(w http.ResponseWriter) {
				w.WriteHeader(200)
				_, _ = io.WriteString(w, appBody)
			}))
			return wireServer{router: r, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter { return &cyclicWriter{w, new(atomic.Int64)} })}, target
		})
		t.Run("wire", func(t *testing.T) {
			assert.True(t, res.ran)
			assert.Equal(t, 200, res.status)
			assert.Equal(t, appBody, res.body)
		})
		t.Run("events", func(t *testing.T) { assert.Empty(t, res.events) })
		t.Run("route", func(t *testing.T) { assert.Equal(t, []string{"/items/{id}"}, res.routes) })
	})

	t.Run("flush-error", func(t *testing.T) {
		res := wireOnce(t, []string{ruleHeaderBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			r, target := simpleRouter(c.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
				assert.Error(t, http.NewResponseController(w).Flush())
			}))
			return wireServer{router: r, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter {
				return flushErrorWriter{ResponseWriter: w, err: errors.New("flush failed")}
			})}, target
		})
		assert.Equal(t, 403, res.status, "a flush error does not mark the response as sent")
		assert.EqualValues(t, 1, res.requestBlocked())
		assert.Empty(t, res.log)
	})

	// A controller flush that reaches a writer that does not send it must not mark the
	// response as sent: the block works as without the tracker (403).
	for _, c := range []struct {
		name string
		wrap func(http.ResponseWriter) http.ResponseWriter
	}{
		// like gzhttp behind a writer that only has Unwrap(): the flush is ignored
		{"ignored-flush-behind-unwrap", func(w http.ResponseWriter) http.ResponseWriter {
			return unwrapOnly{&ignoreFlushWriter{ResponseWriter: w}}
		}},
		// a writer that has FlushError only and keeps the flush
		{"ignored-flusherror", func(w http.ResponseWriter) http.ResponseWriter { return bufferedFlushErrorWriter{w} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := wireOnce(t, []string{ruleHeaderBlock}, func(cap *opCapture, ran *bool) (wireServer, string) {
				r, target := simpleRouter(cap.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
					_ = http.NewResponseController(w).Flush()
				}))
				return wireServer{router: r, wrap: wrapWriter(c.wrap)}, target
			})
			assert.Equal(t, 403, res.status, "the writer did not send the flush")
			assert.Contains(t, res.body, `{"errors":`)
			assert.EqualValues(t, 1, res.requestBlocked())
			assert.Zero(t, res.blockFailure())
		})
	}

	// A direct Flush() through the tracker (no unwrapOnly): a writer below chi that
	// ignores the flush must not mark the response as sent. The result is the result
	// of main: 200 (chi's status after a flush) and the block body.
	for _, h2 := range []bool{false, true} {
		for _, kind := range []string{"flusher", "controller"} {
			name := "direct-ignored-flush-" + kind
			if h2 {
				name += "-h2"
			}
			t.Run(name, func(t *testing.T) {
				res := wireOnce(t, []string{ruleHeaderBlock}, func(cap *opCapture, ran *bool) (wireServer, string) {
					r, target := simpleRouter(cap.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
						if kind == "flusher" {
							w.(http.Flusher).Flush()
						} else {
							_ = http.NewResponseController(w).Flush()
						}
					}))
					return wireServer{router: r, http2: h2, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter {
						return &ignoreFlushWriter{ResponseWriter: w}
					})}, target
				})
				assert.Contains(t, res.body, `{"errors":`, "the block body is written, as on main")
				assert.EqualValues(t, 1, res.requestBlocked())
				assert.Zero(t, res.blockFailure())
			})
		}
	}

	// A direct Flush() that reaches a known server writer is on the wire: no block
	// body follows it.
	for _, h2 := range []bool{false, true} {
		name := "direct-known-writer"
		if h2 {
			name += "-h2"
		}
		t.Run(name, func(t *testing.T) {
			res := wireOnce(t, []string{ruleHeaderBlock}, func(cap *opCapture, ran *bool) (wireServer, string) {
				r, target := simpleRouter(cap.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
					w.(http.Flusher).Flush()
				}))
				return wireServer{router: r, http2: h2}, target
			})
			assert.Equal(t, 200, res.status)
			assert.Empty(t, res.body, "no block body after the flushed response")
			assert.Zero(t, res.requestBlocked())
			assert.EqualValues(t, 1, res.blockFailure())
		})
	}

	// An Unwrap() method that returns another writer after the flush: the writer
	// that got the flush is classified, not the writer that Unwrap() returns later.
	// The empty stage gets exactly 1 flush call, sends nothing, and leaves the chain.
	for _, h2 := range []bool{false, true} {
		name := "flush-target-changes"
		if h2 {
			name += "-h2"
		}
		t.Run(name, func(t *testing.T) {
			var stage *emptyStageWriter
			res := wireOnce(t, []string{ruleHeaderBlock}, func(cap *opCapture, ran *bool) (wireServer, string) {
				r, target := simpleRouter(cap.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
					assert.NoError(t, http.NewResponseController(w).Flush())
				}))
				return wireServer{router: r, http2: h2, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter {
					outer := &switchWriter{}
					stage = &emptyStageWriter{ResponseWriter: w, owner: outer}
					outer.ResponseWriter = stage
					return outer
				})}, target
			})
			assert.Equal(t, 1, stage.calls, "the empty stage gets exactly 1 flush call")
			assert.Equal(t, 403, res.status, "the empty stage sent nothing")
			assert.Contains(t, res.body, `{"errors":`)
			assert.EqualValues(t, 1, res.requestBlocked())
			assert.Zero(t, res.blockFailure())
		})
	}

	// The flush walk has 1 more step than newCommitTracker: the chi writer. With 15
	// Unwrap()-only wrappers above the server writer, the tracker is complete and
	// the flush is marked: no block body follows the sent response. With 1 more
	// wrapper, the tracker is not complete: the flush is not tracked, as on main
	// (the sent response is followed by the block body, and the server logs it).
	for _, c := range []struct {
		name     string
		wrappers int
		tracked  bool
	}{
		{"flush-depth-limit", maxUnwrapWalk - 1, true},
		{"flush-depth-over-limit", maxUnwrapWalk, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := startAppSecRules(t, ruleHeaderBlock)
			cap := newOpCapture()
			var ran bool
			r, target := simpleRouter(cap.middleware, true, readyHandler(&ran, func(w http.ResponseWriter) {
				_ = http.NewResponseController(w).Flush()
			}))
			s := wireServer{router: r, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter {
				for range c.wrappers {
					w = unwrapOnly{w}
				}
				return w
			})}
			res := s.do(t, rec, cap, http.MethodGet, target)
			assert.Equal(t, 200, res.status, "the flush sent the response")
			if c.tracked {
				assert.Empty(t, res.body, "no block body after the flushed response")
				assert.Empty(t, res.log)
				assert.EqualValues(t, 1, res.blockFailure())
			} else {
				assert.Contains(t, res.body, `{"errors":`, "state of main")
				assert.Contains(t, res.log, "superfluous")
			}
		})
	}

	// A controller flush that reaches a known server writer is on the wire: the
	// block cannot replace the response, and no block body follows it.
	for _, h2 := range []bool{false, true} {
		name := "known-writer-behind-unwrap"
		if h2 {
			name += "-h2"
		}
		t.Run(name, func(t *testing.T) {
			res := wireOnce(t, []string{ruleHeaderBlock}, func(cap *opCapture, ran *bool) (wireServer, string) {
				r, target := simpleRouter(cap.middleware, true, readyHandler(ran, func(w http.ResponseWriter) {
					_ = http.NewResponseController(w).Flush()
				}))
				return wireServer{router: r, http2: h2, wrap: wrapWriter(func(w http.ResponseWriter) http.ResponseWriter { return unwrapOnly{w} })}, target
			})
			assert.Equal(t, 200, res.status)
			assert.Empty(t, res.body, "no block body after the flushed response")
			assert.Empty(t, res.log)
			assert.Zero(t, res.requestBlocked())
			assert.EqualValues(t, 1, res.blockFailure())
		})
	}

	// An output buffer for the outer part, the real writer for the inner part, the
	// same request context: the inner tracker must not take the state of the
	// buffer. This block works before this change.
	t.Run("buffer", func(t *testing.T) {
		type realWriterKey struct{}
		res := wireOnce(t, []string{ruleHeaderBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			r := chi.NewRouter()
			r.Use(c.middleware, Middleware(), func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.WriteHeader(201) // on the buffer
					next.ServeHTTP(req.Context().Value(realWriterKey{}).(http.ResponseWriter), req)
				})
			})
			child := chi.NewRouter()
			child.Use(Middleware())
			child.Get("/items/{id}", readyHandler(ran, nil))
			r.Mount("/api", child)
			return wireServer{router: r, wrap: func(w http.ResponseWriter, req *http.Request) (http.ResponseWriter, *http.Request) {
				return httptest.NewRecorder(), req.WithContext(context.WithValue(req.Context(), realWriterKey{}, w))
			}}, "/api/items/clean"
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.True(t, res.ran)
		assert.Equal(t, 403, res.status)
		assert.Contains(t, res.body, `{"errors":`)
		assert.EqualValues(t, 1, res.requestBlocked())
		assert.Empty(t, res.log)
	})
}

// flushMiddleware flushes the response, then calls the next handler.
func flushMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		next.ServeHTTP(w, r)
	})
}

// hideWriter gives the next handler a writer without Unwrap() or optional
// interfaces: the writer chain does not reach the tracer anymore.
func hideWriter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(opaqueWriter{w}, r)
	})
}

// TestAppSecWireHiddenParent covers a writer that hides the outer tracer from the
// inner one: they are in the same request, but the writer chain does not link
// them. The inner tracker is not complete: it gets no newly discovered path
// parameters. The documented limits are the rows with a flush.
func TestAppSecWireHiddenParent(t *testing.T) {
	// mounted: root Use(tracer, [flush], hide), Mount("/api", child), child
	// Use(tracer, [StripSlashes]). With the trailing slash, the root lookup does not
	// match, so the root gets "id" after the handler. Without it, it gets "id" before.
	mounted := func(c *opCapture, ran *bool, flush, trailingSlash bool) (wireServer, string) {
		r := chi.NewRouter()
		r.Use(c.middleware, Middleware())
		if flush {
			r.Use(flushMiddleware)
		}
		r.Use(hideWriter)
		child := chi.NewRouter()
		child.Use(Middleware())
		target := "/api/items/attack"
		if trailingSlash {
			child.Use(middleware.StripSlashes)
			target += "/"
		}
		child.Get("/items/{id}", readyHandler(ran, nil))
		r.Mount("/api", child)
		return wireServer{router: r}, target
	}

	t.Run("hidden-parent", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, true, true)
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		// The inner tracker cannot share the baseline input of the root tracer (the
		// writer chain does not reach it) but it still gives its known parameters to
		// the same WAF context: it marks the shared input as uncertain, so the root
		// reports only its route after the handler. The WAF never gets "id": the same
		// result as before this change (no match, no event, no block).
		assert.Equal(t, 200, res.status)
		assert.Empty(t, res.body)
		assert.Empty(t, res.log)
		assert.Empty(t, res.events)
		assert.Zero(t, res.requestBlocked())
		assert.Zero(t, res.blockFailure())
	})

	t.Run("hidden-parent-none", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, false, true)
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		// As above: the root reports only its route, so no rule on "id" matches.
		assert.Equal(t, 200, res.status)
		assert.Empty(t, res.events)
		assert.Empty(t, res.log)
		assert.Zero(t, res.requestBlocked())
		assert.Zero(t, res.blockFailure())
	})

	// The inner tracer starts after chi routed the mount, so the path parameters
	// that it knows ({"*": ""}) replace the "id" that the root lookup gave to the
	// WAF. The inner tracker is not complete: it does not add the final "id". The
	// root sees no change at the end, so it does not run the WAF again. Whether the
	// rule matches depends on the order of its conditions (a WAF detail): with the
	// response header first, the path parameter is not evaluated before the
	// response, when "id" is no longer there: no match.
	t.Run("hidden-parent-early", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlockHeaderFirst}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, true, false)
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.Equal(t, 200, res.status)
		assert.Empty(t, res.body)
		assert.Empty(t, res.log)
		assert.Empty(t, res.events)
		assert.Zero(t, res.requestBlocked())
		assert.Zero(t, res.blockFailure())
	})

	// With the path parameter first, the WAF kept the match on "id" from the root
	// lookup: this is the documented remaining risk (the inner tracker does not see
	// the flush).
	t.Run("hidden-parent-early-path-first", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, true, false)
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.Equal(t, 200, res.status)
		assert.Contains(t, res.body, `{"errors":`, "documented limit")
		assert.Equal(t, []string{"mixed-block"}, res.events)
		assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
	})

	// The same existing failure as before this change: a rule on a response header
	// adds the block body after a flushed response, because the inner tracker does
	// not see the flush. The WAF has no path parameters in this rule.
	t.Run("hidden-parent-early-H", func(t *testing.T) {
		res := wireOnce(t, []string{ruleHeaderBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, true, false)
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}", "/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.Equal(t, 200, res.status)
		assert.Contains(t, res.body, `{"errors":`, "documented limit (this exists without this change)")
		assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
	})

	// The remaining risk: 2 tracers before routing on the same router, a flush and a
	// hidden writer between them, and a rule on path parameters and the response.
	// The outer lookup gave "id" to the WAF; no mount parameters replace it.
	t.Run("hidden-parent-adjacent", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlockHeaderFirst}, func(c *opCapture, ran *bool) (wireServer, string) {
			r := chi.NewRouter()
			r.Use(c.middleware, Middleware(), flushMiddleware, hideWriter, Middleware())
			r.Get("/items/{id}", readyHandler(ran, nil))
			return wireServer{router: r}, "/items/attack"
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/items/{id}", "/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.Equal(t, 200, res.status)
		assert.Contains(t, res.body, `{"errors":`, "documented limit")
		assert.Empty(t, res.log)
		assert.Equal(t, []string{"mixed-block"}, res.events)
		assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
	})

	// The context is replaced after the tracer (the chi route context is kept) and
	// the writer chain is hidden: the inner tracker finds no outer tracker, so it is
	// complete. It gives the final "id" and adds a block body after the flushed
	// response (documented limit: no outer tracker can be detected).
	t.Run("hidden-parent-reset-context", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlockHeaderFirst}, func(c *opCapture, ran *bool) (wireServer, string) {
			r := chi.NewRouter()
			r.Use(c.middleware, Middleware(), flushMiddleware, hideWriter, replaceContext)
			child := chi.NewRouter()
			child.Use(Middleware(), middleware.StripSlashes)
			child.Get("/items/{id}", readyHandler(ran, nil))
			r.Mount("/api", child)
			return wireServer{router: r}, "/api/items/attack/"
		})
		t.Run("route", func(t *testing.T) {
			assert.Equal(t, []string{"/api/items/{id}"}, res.routes, "the AppSec route of each operation")
		})
		assert.Equal(t, 200, res.status)
		assert.Contains(t, res.body, `{"errors":`, "documented limit")
		assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
	})
}

// TestAppSecWireStaleParams documents that AppSec cannot remove path parameters
// that it already has. The early lookup gave "id" to the WAF; a middleware after
// the tracer then routes the request to a route without parameters. The late run
// has nothing to give (the WAF ignores an empty set of parameters), so the
// response rule can still match with the early "id".
func TestAppSecWireStaleParams(t *testing.T) {
	res := wireOnce(t, []string{ruleMixedBlockHeaderFirst}, func(c *opCapture, ran *bool) (wireServer, string) {
		r := chi.NewRouter()
		r.Use(c.middleware, Middleware(), func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				chi.RouteContext(req.Context()).RoutePath = "/health"
				next.ServeHTTP(w, req)
			})
		})
		r.Get("/items/{id}", func(http.ResponseWriter, *http.Request) {})
		r.Get("/health", readyHandler(ran, nil))
		return wireServer{router: r}, "/items/attack"
	})
	assert.True(t, res.ran, "the handler of /health ran")
	assert.Equal(t, 403, res.status, "documented limit: blocked with the early parameter")
	assert.Equal(t, []string{"mixed-block"}, res.events)
	assert.Equal(t, []string{"/health"}, res.routes, "the route is the route that chi selected")
}

// TestAppSecWireCompress uses the real chi v1.5.5 middleware.Compress as the
// writer that hides the outer tracer: it has no Unwrap().
func TestAppSecWireCompress(t *testing.T) {
	mounted := func(c *opCapture, ran *bool, trailingSlash bool) (wireServer, string) {
		r := chi.NewRouter()
		r.Use(c.middleware, Middleware(), flushMiddleware, middleware.Compress(5))
		child := chi.NewRouter()
		child.Use(Middleware())
		target := "/api/items/attack"
		if trailingSlash {
			child.Use(middleware.StripSlashes)
			target += "/"
		}
		child.Get("/items/{id}", readyHandler(ran, nil))
		r.Mount("/api", child)
		return wireServer{router: r}, target
	}

	// The inner tracer cannot share the baseline input with the root (no Unwrap()
	// in Compress), so the root reports only its route: no rule on "id" matches,
	// as before this change.
	t.Run("hidden-parent", func(t *testing.T) {
		res := wireOnce(t, []string{ruleMixedBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, true)
		})
		assert.Equal(t, 200, res.status)
		assert.Empty(t, res.body)
		assert.Empty(t, res.log)
		assert.Empty(t, res.events)
		assert.Zero(t, res.requestBlocked())
		assert.Zero(t, res.blockFailure())
	})

	// A rule on a response header only: the same failure as before this change.
	t.Run("hidden-parent-early-H", func(t *testing.T) {
		res := wireOnce(t, []string{ruleHeaderBlock}, func(c *opCapture, ran *bool) (wireServer, string) {
			return mounted(c, ran, false)
		})
		assert.Equal(t, 200, res.status)
		assert.Contains(t, res.body, `{"errors":`, "documented limit (this exists without this change)")
		assert.EqualValues(t, 1, res.requestBlocked(), "a false report")
	})
}

// switchWriter drops an empty stage from its chain after the stage got a flush.
type switchWriter struct{ http.ResponseWriter }

func (w *switchWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// emptyStageWriter is a buffering stage with FlushError() that sends nothing.
type emptyStageWriter struct {
	http.ResponseWriter
	owner *switchWriter
	calls int
}

func (w *emptyStageWriter) FlushError() error {
	w.calls++
	w.owner.ResponseWriter = w.ResponseWriter
	return nil
}
