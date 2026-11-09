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
	"net"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
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
	middleware.WrapResponseWriter // chi's Status, BytesWritten, Tee, Discard, Header, Write, WriteHeader, Unwrap

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
// that writer. Only a nil result marks the response as sent.
func (t *commitTracker) flushError() error {
	err := http.NewResponseController(t.WrapResponseWriter).Flush()
	if err == nil {
		t.markFlushed()
	}
	return err
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
	case ct.parent == nil && ctx != nil && ctx.Value(trackerCtxKey{}) != nil:
		// An outer tracer handles this request, but the writer chain does not
		// reach it: a writer without Unwrap() or another writer was put in
		// between. They may not write to the same response.
		ct.complete = false
	}

	return wrapCommitTracker(ct, flags), ct
}
