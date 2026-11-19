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
	"io"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// maxUnwrapWalk is the maximum number of writers that newCommitTracker visits
// when it follows the Unwrap() chain under the chi writer (the parent walk). A
// longer chain (or a cycle) ends the walk: the tracker then has no parent and no
// FlushError from the writers that it did not visit. The flush walk of flushError
// also visits the chi writer, so it has 1 more step.
const maxUnwrapWalk = 16

// commitTracker records when the response of a request started, so that AppSec
// does not add a block response after bytes that were already sent. chi's
// middleware.WrapResponseWriter misses some of the ways a response starts
// (Flush and Hijack, also through http.ResponseController), so the commitTracker
// wraps the chi writer and records those. It is only used when AppSec is
// enabled.
//
// A tracker finds an outer tracker only through the Unwrap() chain, which proves
// that both write to the same response. A writer without Unwrap() between 2
// trackers hides the outer one. So the middlewares after Middleware must send the
// response through the writer that they get and return it from Unwrap(), and a
// middleware that writes or flushes before Middleware is not visible.
//
// A hijack is recorded only if the chi writer is a Hijacker. If the connection can be
// hijacked only through Unwrap() (a writer with only http.ResponseWriter and
// Unwrap() under chi), http.ResponseController.Hijack reaches it without the tracker,
// so Committed() stays false and a late block writes to the hijacked connection (as
// before this change).
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

func (t *commitTracker) markFlushed() {
	t.flushed = true
	if t.flushStatus == 0 {
		t.flushStatus = t.WrapResponseWriter.Status()
		if t.flushStatus == 0 {
			t.flushStatus = http.StatusOK
		}
	}
}

// flush is the Flush method of the trackers whose chi writer has Flush. This is the
// direct Flush path: chi's writer sends the flush to its immediate target, which is
// the writer that is classified (see flushError for the rule). A flush that nothing
// sends does not mark the response as sent, as without the tracker.
//
// Since chi v5.4, the chi writer also sends WriteHeader(200) in Flush
// (basicWriter.flush calls maybeWriteHeader), before the flush is classified.
// A writer below that is not a known server writer is not proof of an ignored
// flush: it can forward the flush (for example gzhttp.NoGzipResponseWriter),
// and net/http locks the status and the headers at that WriteHeader. The
// response is then not replaceable, so the status counts as a commit, like the
// state of chi without the tracker: a late block does not run, and the tracker
// reports a block failure instead of a false request_blocked. A safe replacement
// for the lost late block is a follow-up.
func (t *commitTracker) flush() {
	target := t.WrapResponseWriter.Unwrap()
	t.WrapResponseWriter.(http.Flusher).Flush()
	if flushIsOnWire(target) {
		t.markFlushed()
	}
}

// flushError is the FlushError method of the trackers whose chi writer has no
// Flush method, but a writer under it can flush. The result is the result of
// that writer. It marks the response as sent only if the flush is known to be on
// the wire: an opaque writer can ignore a flush, or buffer it, and still return
// nil, and a block that works without the tracker would be lost.
//
// This is the http.ResponseController path, for a tracker without Flush. It does
// the walk of http.ResponseController.Flush (FlushError, then Flush, then Unwrap)
// itself, in one pass, from the chi writer: the writer that gets the flush is the writer that
// is classified. A second walk could find another writer, if an Unwrap method
// returns a different writer after the flush. The first writer that flushes is
// on the wire if it is a known server writer (httptrace.KnownServerWriter: its Flush
// always sends the status line and the headers, or locks the status of a recorder),
// or an outer tracker
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

// flushIsOnWire reports whether a flush that w got reached the wire: w is an outer
// tracker that recorded a flush, or a known server writer
// (httptrace.KnownServerWriter owns the list of the types, which change with the
// Go version).
func flushIsOnWire(w http.ResponseWriter) bool {
	if tc, ok := w.(trackerCarrier); ok {
		return tc.tracker().flushed
	}
	return httptrace.KnownServerWriter(w)
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
// http.Pusher), plus FlushError() only when ww has no Flush() and a writer under
// it can flush: http.ResponseController would otherwise bypass the tracker. It
// also implements middleware.WrapResponseWriter, and Unwrap() returns the same
// target as ww.
func newCommitTracker(ww middleware.WrapResponseWriter) http.ResponseWriter {
	ct := &commitTracker{WrapResponseWriter: ww}

	lowerFlush := false // a writer under ww declares FlushError or Flush
	cur := ww.Unwrap()
	for steps := 0; cur != nil && steps < maxUnwrapWalk; steps++ {
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
	if _, ok := ww.(http.Hijacker); ok {
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

	return wrapCommitTracker(ct, flags)
}
