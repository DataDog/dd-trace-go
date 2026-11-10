// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

// The generator output goes through a temporary file, so that a failure of the
// generator fails go generate.
//
//go:generate sh -c "set -e; trap 'rm -f writer_appsec_gen.go.tmp' EXIT; go run make_writer_appsec.go > writer_appsec_gen.go.tmp; gofmt writer_appsec_gen.go.tmp > writer_appsec_gen.go"

import (
	"bufio"
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"reflect"

	"github.com/go-chi/chi/middleware"
)

// maxUnwrapWalk is the maximum number of writers that newCommitTracker visits
// when it follows the Unwrap() chain under the chi writer. A longer chain (or a
// cycle) makes the tracker incomplete.
const maxUnwrapWalk = 16

// trackerCtxKey is the request context key of the commitTracker of the nearest
// outer AppSec-enabled Middleware. The context only detects that an outer
// tracker exists for the request: it never provides state, because a middleware
// can replace the response writer and keep the request context.
type trackerCtxKey struct{}

// commitTracker records when the response of a request started, so that AppSec
// does not add a block response after bytes that were already sent. chi's
// middleware.WrapResponseWriter misses some of the ways a response starts
// (Flush and Hijack, also through http.ResponseController), so the commitTracker
// wraps the chi writer and records those. It is only used when AppSec is
// enabled.
//
// A commitTracker is not safe for concurrent use, like the writer that it wraps.
type commitTracker struct {
	middleware.WrapResponseWriter // chi's Status, BytesWritten, Tee, Header, Write, WriteHeader, Unwrap

	// parent is the nearest outer commitTracker, reached through the Unwrap()
	// chain under the chi writer. It writes to the same response. It is nil
	// when the chain has none.
	parent *commitTracker

	flushed  bool
	hijacked bool

	// flushStatus is the status of the response at the first successful flush: the
	// status that chi had, or 200 (what the client got). A later WriteHeader does
	// not change it: the wire already has the status.
	flushStatus int

	// complete is false when the tracker cannot see every way the response can
	// start, so the WAF gets no newly discovered path parameters for it.
	complete bool

	// shared is the path parameters that the tracers of this request gave to the
	// WAF as the baseline input (see sharedInput). All the trackers of a request
	// have the same one: a tracker takes it from its parent, which is reached
	// through the writer chain and the request context. A tracker that cannot
	// reach the one of the outer tracers has its own, and is not complete: it marks
	// the shared input of the outer tracers as uncertain.
	shared *sharedInput
}

// sharedInput is the path parameters that AppSec gave to the WAF as its baseline
// input: what the tracers of the request knew when they started (the input of
// AppSec before the route lookup existed), and what they reported after the
// handler. Early predictions are not recorded in it.
//
// A tracer replaces the baseline input at its entry with what it knows, as it did
// before the route lookup existed. Everything else must pass a conflict check
// against it: a prediction or a late report adds new names, but it never changes
// a value in the set, because that would change the result of a rule that the WAF
// evaluates later, or combine the values of 2 tracers. The tracers of a request
// run one after the other, so no lock is needed.
type sharedInput struct {
	params map[string]string

	// uncertain is set when a tracer that shares the WAF context of the tracers of
	// this set could not share the set (see newCommitTracker): it can give baseline
	// input that the set does not have. The tracers that share the set then report
	// only the route.
	uncertain bool
}

func (s *sharedInput) get() map[string]string { return s.params }

func (s *sharedInput) set(params map[string]string) { s.params = maps.Clone(params) }

// markUncertain sets the flag of the shared input of t and of every outer tracker
// that t reaches through the writer chain.
func (t *commitTracker) markUncertain() {
	for ; t != nil; t = t.parent {
		t.shared.uncertain = true
	}
}

// trackerCarrier is implemented by all the generated tracker types.
type trackerCarrier interface {
	tracker() *commitTracker
}

func (t *commitTracker) tracker() *commitTracker { return t }

// Committed reports whether the response is sent (or recorded as sent by a
// writer that it can see). httpsec uses it to decide if a block response can
// still replace the response.
func (t *commitTracker) Committed() bool {
	return t.flushed || t.hijacked ||
		t.WrapResponseWriter.Status() != 0 ||
		t.WrapResponseWriter.BytesWritten() > 0 ||
		(t.parent != nil && t.parent.Committed())
}

// Status returns the status of the response. chi reports 0 after a Flush; this
// reports 200 (what the client got). With a parent tracker that knows a status,
// the parent is closer to the wire: it wins.
func (t *commitTracker) Status() int {
	if p := t.parent; p != nil && p.Committed() {
		if s := p.Status(); s != 0 {
			return s
		}
	}
	if t.flushStatus != 0 {
		return t.flushStatus
	}
	if s := t.WrapResponseWriter.Status(); s != 0 {
		return s
	}
	return 0
}

// markFlushed records a successful flush.
func (t *commitTracker) markFlushed() {
	t.flushed = true
	if t.flushStatus == 0 {
		t.flushStatus = t.WrapResponseWriter.Status()
		if t.flushStatus == 0 {
			t.flushStatus = http.StatusOK
		}
	}
}

func (t *commitTracker) flush() {
	t.WrapResponseWriter.(http.Flusher).Flush()
	t.markFlushed()
}

// flushError is the FlushError method of the trackers whose chi writer has no
// Flush method, but a writer under it can flush. The result is the result of
// that writer. It marks the response as sent only if the flush is known to be on
// the wire: an opaque writer can ignore a flush, or buffer it, and still return
// nil, and a block that works without the tracker would be lost.
//
// It does the walk of http.ResponseController.Flush (FlushError, then Flush, then
// Unwrap) itself, in one pass: the writer that gets the flush is the writer that
// is classified. A second walk could find another writer, if an Unwrap method
// returns a different writer after the flush. The first writer that flushes is
// on the wire if it is a known server writer (its Flush always sends the status
// line and the headers, or locks the status of a recorder), or an outer tracker
// that recorded the flush. For any other writer (a compressing writer that
// ignores a flush before the first write, a buffering writer, ...), the state
// does not change: it is the state without the tracker.
func (t *commitTracker) flushError() error {
	w := http.ResponseWriter(t.WrapResponseWriter)
	// The chi writer is the first step: newCommitTracker does not count it.
	for range maxUnwrapWalk + 1 {
		switch f := w.(type) {
		case interface{ FlushError() error }:
			err := f.FlushError()
			if err == nil && flushIsOnWire(w) {
				t.markFlushed()
			}
			return err
		case http.Flusher:
			f.Flush()
			if flushIsOnWire(w) {
				t.markFlushed()
			}
			return nil
		case unwrapper:
			if w = f.Unwrap(); w == nil {
				return http.ErrNotSupported
			}
		default:
			return http.ErrNotSupported
		}
	}
	// The bounded walk ends (a cycle or a very long chain): the controller
	// finishes the walk, and the state does not change.
	return http.NewResponseController(w).Flush()
}

// flushIsOnWire reports whether a flush that w got reached the wire.
func flushIsOnWire(w http.ResponseWriter) bool {
	if tc, ok := w.(trackerCarrier); ok {
		return tc.tracker().flushed
	}
	return knownServerWriter(w)
}

// knownServerWriters are the writers whose Flush always sends the headers
// ("package path.type name"). Their types are not exported, and their names depend
// on the Go version (the HTTP/2 writer is a pointer type in Go 1.26 and a value
// type in Go 1.27), so they are compared by name, pointer or value. A renamed type
// is not known: the tracker then keeps the state without the tracker, and the test
// of the writers of this Go version fails.
var knownServerWriters = map[string]bool{
	"net/http.response":                     true, // HTTP/1
	"net/http.http2responseWriter":          true, // HTTP/2 (Go 1.26)
	"net/http.http2ResponseWriter":          true, // HTTP/2 (Go 1.27)
	"golang.org/x/net/http2.responseWriter": true, // h2c and x/net HTTP/2
	"net/http/httptest.ResponseRecorder":    true,
}

func knownServerWriter(w http.ResponseWriter) bool {
	typ := reflect.TypeOf(w)
	if typ == nil {
		return false
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return knownServerWriters[typ.PkgPath()+"."+typ.Name()]
}

func (t *commitTracker) hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := t.WrapResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		t.hijacked = true
	}
	return conn, rw, err
}

func (t *commitTracker) readFrom(r io.Reader) (int64, error) {
	return t.WrapResponseWriter.(io.ReaderFrom).ReadFrom(r)
}

func (t *commitTracker) push(target string, opts *http.PushOptions) error {
	return t.WrapResponseWriter.(http.Pusher).Push(target, opts)
}

// Bits of the interface set of a tracker. They select the generated type.
const (
	hasFlusher uint8 = 1 << iota
	hasHijacker
	hasReaderFrom
	hasPusher
	hasFlushError
)

type unwrapper interface{ Unwrap() http.ResponseWriter }

// newCommitTracker wraps the chi writer ww in a commit tracker. The result has
// exactly the interface set of ww (http.Flusher, http.Hijacker, io.ReaderFrom,
// http.Pusher), plus FlushError() only when ww is not an http.Flusher and a
// writer under it can flush: http.ResponseController would otherwise bypass the
// tracker. It also implements middleware.WrapResponseWriter, and Unwrap()
// returns the same target as ww.
//
// ctx is the request context. It only detects that an outer tracker handles the
// same request (see trackerCtxKey).
func newCommitTracker(ww middleware.WrapResponseWriter, ctx context.Context) (http.ResponseWriter, *commitTracker) {
	ct := &commitTracker{WrapResponseWriter: ww, complete: true}

	var (
		lowerHijacker bool // a writer under ww can hijack, before the walk ends or finds a tracker
		lowerFlush    bool // a writer under ww declares FlushError or Flush
		truncated     bool
	)
	cur := ww.Unwrap()
	for steps := 0; cur != nil; steps++ {
		if steps == maxUnwrapWalk {
			truncated = true
			break
		}
		if _, ok := cur.(interface{ FlushError() error }); ok {
			lowerFlush = true
		} else if _, ok := cur.(http.Flusher); ok {
			lowerFlush = true
		}
		if tc, ok := cur.(trackerCarrier); ok {
			// The same response: the state of this writer is read live.
			ct.parent = tc.tracker()
			break
		}
		if _, ok := cur.(http.Hijacker); ok {
			lowerHijacker = true
		}
		u, ok := cur.(unwrapper)
		if !ok {
			break
		}
		cur = u.Unwrap()
	}

	var outer *commitTracker
	if ctx != nil {
		outer, _ = ctx.Value(trackerCtxKey{}).(*commitTracker)
	}

	var flags uint8
	if _, ok := ww.(http.Flusher); ok {
		flags |= hasFlusher
	}
	_, isHijacker := ww.(http.Hijacker)
	if isHijacker {
		flags |= hasHijacker
	}
	if _, ok := ww.(io.ReaderFrom); ok {
		flags |= hasReaderFrom
	}
	if _, ok := ww.(http.Pusher); ok {
		flags |= hasPusher
	}
	if flags&hasFlusher == 0 && lowerFlush {
		flags |= hasFlushError
	}

	switch {
	case truncated:
		// A cycle or a very long chain.
		ct.complete = false
	case lowerHijacker && !isHijacker:
		// http.ResponseController.Hijack would bypass the tracker.
		ct.complete = false
	case ct.parent != nil && !ct.parent.complete:
		ct.complete = false
	case ct.parent == nil && outer != nil:
		// An outer tracer handles this request, but the writer chain does not
		// reach it: a writer without Unwrap() or another writer was put in
		// between. They may not write to the same response.
		ct.complete = false
	case ct.parent != nil && outer != ct.parent:
		// The writer chain reaches an outer tracer, but the request context does not
		// give it (a middleware replaced the context): this tracker cannot reach the
		// shared input of the outer tracers.
		ct.complete = false
	}
	if ct.parent != nil && outer == ct.parent {
		ct.shared = ct.parent.shared
	} else {
		// The first tracer of the request, or one that cannot reach the shared input.
		ct.shared = &sharedInput{}
		// In the second case, this tracer can still give its known parameters to the
		// WAF context of the outer tracers (it keeps its original behavior): they
		// must not report parameters that contradict it without knowing it. The outer
		// tracers that it finds through the writer chain and through the request
		// context get the flag.
		ct.parent.markUncertain()
		outer.markUncertain()
	}

	return wrapCommitTracker(ct, flags), ct
}
