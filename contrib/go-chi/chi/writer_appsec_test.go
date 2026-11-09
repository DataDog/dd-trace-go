// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/go-chi/chi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRaw is the writer that net/http would give: it records what is sent. It
// has all the optional methods; rawWith hides those that a test does not want.
type fakeRaw struct {
	header    http.Header
	codes     []int
	body      bytes.Buffer
	flushes   int
	hijacks   int
	hijackErr error
	flushErr  error // result of FlushError; the flush is only recorded when nil
}

func newFakeRaw() *fakeRaw { return &fakeRaw{header: http.Header{}} }

func (f *fakeRaw) Header() http.Header  { return f.header }
func (f *fakeRaw) WriteHeader(code int) { f.codes = append(f.codes, code) }
func (f *fakeRaw) Write(b []byte) (int, error) {
	if len(f.codes) == 0 {
		f.codes = append(f.codes, http.StatusOK)
	}
	return f.body.Write(b)
}
func (f *fakeRaw) Flush() {
	f.flushes++
	if len(f.codes) == 0 {
		f.codes = append(f.codes, http.StatusOK)
	}
}
func (f *fakeRaw) FlushError() error {
	if f.flushErr != nil {
		return f.flushErr
	}
	f.Flush()
	return nil
}
func (f *fakeRaw) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	f.hijacks++
	return nil, nil, f.hijackErr
}
func (f *fakeRaw) ReadFrom(r io.Reader) (int64, error) {
	if len(f.codes) == 0 {
		f.codes = append(f.codes, http.StatusOK)
	}
	return io.Copy(&f.body, r)
}
func (f *fakeRaw) Push(string, *http.PushOptions) error { return nil }

const (
	rawFlusher = 1 << iota
	rawHijacker
	rawReaderFrom
	rawPusher
)

// rawWith returns f with only the optional interfaces of the set.
func rawWith(f *fakeRaw, set int) http.ResponseWriter {
	switch set {
	case 0:
		return struct {
			http.ResponseWriter
		}{f}
	case 1:
		return struct {
			http.ResponseWriter
			http.Flusher
		}{f, f}
	case 2:
		return struct {
			http.ResponseWriter
			http.Hijacker
		}{f, f}
	case 3:
		return struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
		}{f, f, f}
	case 4:
		return struct {
			http.ResponseWriter
			io.ReaderFrom
		}{f, f}
	case 5:
		return struct {
			http.ResponseWriter
			http.Flusher
			io.ReaderFrom
		}{f, f, f}
	case 6:
		return struct {
			http.ResponseWriter
			http.Hijacker
			io.ReaderFrom
		}{f, f, f}
	case 7:
		return struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
			io.ReaderFrom
		}{f, f, f, f}
	case 8:
		return struct {
			http.ResponseWriter
			http.Pusher
		}{f, f}
	case 9:
		return struct {
			http.ResponseWriter
			http.Flusher
			http.Pusher
		}{f, f, f}
	case 10:
		return struct {
			http.ResponseWriter
			http.Hijacker
			http.Pusher
		}{f, f, f}
	case 11:
		return struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
			http.Pusher
		}{f, f, f, f}
	case 12:
		return struct {
			http.ResponseWriter
			io.ReaderFrom
			http.Pusher
		}{f, f, f}
	case 13:
		return struct {
			http.ResponseWriter
			http.Flusher
			io.ReaderFrom
			http.Pusher
		}{f, f, f, f}
	case 14:
		return struct {
			http.ResponseWriter
			http.Hijacker
			io.ReaderFrom
			http.Pusher
		}{f, f, f, f}
	case 15:
		return struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
			io.ReaderFrom
			http.Pusher
		}{f, f, f, f, f}
	}
	panic("invalid set")
}

// flushErrorOnly has FlushError, but no http.Flusher, and hides the interfaces
// of the writer under it. It has Unwrap(), like most wrappers.
type flushErrorOnly struct{ http.ResponseWriter }

func (w flushErrorOnly) FlushError() error           { return w.ResponseWriter.(*fakeRaw).FlushError() }
func (w flushErrorOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// plain is a wrapper that has Unwrap() and no other optional method.
type plain struct{ http.ResponseWriter }

func (w plain) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// opaque is a wrapper without Unwrap().
type opaque struct{ http.ResponseWriter }

// cyclic returns itself from Unwrap().
type cyclic struct{ http.ResponseWriter }

func (w *cyclic) Unwrap() http.ResponseWriter { return w }

// hijackOnly can hijack and has Unwrap(), but the chi writer over a plain
// wrapper of it is not a Hijacker.
type hijackOnly struct{ http.ResponseWriter }

func (w hijackOnly) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(*fakeRaw).Hijack()
}
func (w hijackOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// track wraps raw in a chi writer and a commit tracker.
func track(t testing.TB, raw http.ResponseWriter, proto int, ctx context.Context) (http.ResponseWriter, *commitTracker, middleware.WrapResponseWriter) {
	t.Helper()
	ww := middleware.NewWrapResponseWriter(raw, proto)
	tw, ct := newCommitTracker(ww, ctx)
	return tw, ct, ww
}

type ifaces struct{ flusher, hijacker, readerFrom, pusher, flushError bool }

func ifacesOf(w any) ifaces {
	var i ifaces
	_, i.flusher = w.(http.Flusher)
	_, i.hijacker = w.(http.Hijacker)
	_, i.readerFrom = w.(io.ReaderFrom)
	_, i.pusher = w.(http.Pusher)
	_, i.flushError = w.(interface{ FlushError() error })
	return i
}

// TestCommitTrackerInterfaces checks that a tracker has the interface set of the
// chi writer that it wraps (a handler must get the same answer with and without
// AppSec), plus FlushError() only when http.ResponseController would otherwise
// bypass the tracker.
func TestCommitTrackerInterfaces(t *testing.T) {
	for set := 0; set < 16; set++ {
		for _, flushUnder := range []bool{false, true} {
			for _, proto := range []int{1, 2} {
				f := newFakeRaw()
				raw := rawWith(f, set)
				if flushUnder {
					raw = flushErrorOnly{raw}
				}
				tw, _, ww := track(t, raw, proto, nil)

				want := ifacesOf(ww)
				_, wwFlusher := ww.(http.Flusher)
				// FlushError exists only when chi's writer has no Flush, and a
				// writer under it can flush (raw has Flush, or FlushError).
				_, rawFlush := rawWith(f, set).(http.Flusher)
				want.flushError = !wwFlusher && (flushUnder || rawFlush)
				assert.Equal(t, want, ifacesOf(tw), "set=%d flushUnder=%v proto=%d", set, flushUnder, proto)

				_, isWrap := tw.(middleware.WrapResponseWriter)
				assert.True(t, isWrap, "a tracker is a middleware.WrapResponseWriter")
				assert.Equal(t, ww.Unwrap(), tw.(middleware.WrapResponseWriter).Unwrap(), "Unwrap returns the target of chi's writer")
			}
		}
	}
}

// fakeChi is a chi writer with every optional interface. It covers the sets that
// this chi version does not produce.
type fakeChi struct {
	middleware.WrapResponseWriter
	*fakeRaw
}

func (f fakeChi) Header() http.Header         { return f.fakeRaw.Header() }
func (f fakeChi) Write(b []byte) (int, error) { return f.fakeRaw.Write(b) }
func (f fakeChi) WriteHeader(c int)           { f.fakeRaw.WriteHeader(c) }
func (f fakeChi) Status() int                 { return 0 }
func (f fakeChi) BytesWritten() int           { return 0 }
func (f fakeChi) Unwrap() http.ResponseWriter { return nil }

// TestCommitTrackerGeneratedTypes builds each of the 24 generated types.
func TestCommitTrackerGeneratedTypes(t *testing.T) {
	n := 0
	for flags := uint8(0); flags < 32; flags++ {
		if flags&hasFlusher != 0 && flags&hasFlushError != 0 {
			continue // no FlushError next to a Flush
		}
		n++
		ct := &commitTracker{WrapResponseWriter: fakeChi{fakeRaw: newFakeRaw()}, complete: true}
		w := wrapCommitTracker(ct, flags)
		assert.Equal(t, ifaces{
			flusher:    flags&hasFlusher != 0,
			hijacker:   flags&hasHijacker != 0,
			readerFrom: flags&hasReaderFrom != 0,
			pusher:     flags&hasPusher != 0,
			flushError: flags&hasFlushError != 0,
		}, ifacesOf(w), "flags=%05b", flags)
		_, ok := w.(trackerCarrier)
		assert.True(t, ok)
		assert.Same(t, ct, w.(trackerCarrier).tracker())
	}
	assert.Equal(t, 24, n)
	assert.Panics(t, func() { wrapCommitTracker(&commitTracker{}, hasFlusher|hasFlushError) })
}

// TestCommitTrackerState checks when a tracker reports that the response is sent.
func TestCommitTrackerState(t *testing.T) {
	full := func() (http.ResponseWriter, *commitTracker, middleware.WrapResponseWriter, *fakeRaw) {
		f := newFakeRaw()
		tw, ct, ww := track(t, rawWith(f, rawFlusher|rawHijacker|rawReaderFrom), 1, nil)
		return tw, ct, ww, f
	}

	t.Run("write", func(t *testing.T) {
		for name, act := range map[string]func(http.ResponseWriter){
			"WriteHeader(200)": func(w http.ResponseWriter) { w.WriteHeader(200) },
			"WriteHeader(101)": func(w http.ResponseWriter) { w.WriteHeader(101) },
			"Write(nil)":       func(w http.ResponseWriter) { _, _ = w.Write(nil) },
		} {
			tw, ct, _, _ := full()
			assert.False(t, ct.Committed(), name)
			act(tw)
			assert.True(t, ct.Committed(), name)
		}
	})

	t.Run("103 early hints", func(t *testing.T) {
		tw, ct, _, _ := full()
		tw.WriteHeader(103)
		// chi v1.5.5 records a 1xx code as the final status and drops the next
		// WriteHeader: the tracker reports "sent", the safe side (a block becomes a
		// failed block).
		assert.True(t, ct.Committed())
	})

	t.Run("flush", func(t *testing.T) {
		tw, ct, ww, f := full()
		tw.(http.Flusher).Flush()
		assert.True(t, ct.Committed())
		assert.Equal(t, 200, ct.Status(), "chi reports 0 after a flush")
		assert.Equal(t, 0, ww.Status())
		assert.Equal(t, 200, tw.(middleware.WrapResponseWriter).Status(), "what a downstream middleware sees")
		assert.Equal(t, 1, f.flushes)
	})

	t.Run("ResponseController flush", func(t *testing.T) {
		tw, ct, _, f := full()
		require.NoError(t, http.NewResponseController(tw).Flush())
		assert.True(t, ct.Committed())
		assert.Equal(t, 1, f.flushes)
	})

	t.Run("FlushError only", func(t *testing.T) {
		f := newFakeRaw()
		tw, ct, _ := track(t, flushErrorOnly{f}, 1, nil)
		_, isFlusher := tw.(http.Flusher)
		require.False(t, isFlusher)
		require.NoError(t, http.NewResponseController(tw).Flush())
		assert.True(t, ct.Committed())
		assert.Equal(t, 200, ct.Status())
	})

	t.Run("flush error", func(t *testing.T) {
		f := newFakeRaw()
		f.flushErr = errors.New("flush failed")
		tw, ct, _ := track(t, flushErrorOnly{f}, 1, nil)
		assert.EqualError(t, http.NewResponseController(tw).Flush(), "flush failed")
		assert.False(t, ct.Committed(), "an error does not itself mark the response as sent")

		// ... and it does not clear a state that is known.
		tw, ct, _ = track(t, flushErrorOnly{f}, 1, nil)
		tw.WriteHeader(200)
		assert.Error(t, http.NewResponseController(tw).Flush())
		assert.True(t, ct.Committed())
	})

	t.Run("flush not supported", func(t *testing.T) {
		tw, ct, _ := track(t, rawWith(newFakeRaw(), 0), 1, nil)
		assert.False(t, ifacesOf(tw).flushError, "nothing under the writer can flush")
		assert.ErrorIs(t, http.NewResponseController(tw).Flush(), http.ErrNotSupported)
		assert.False(t, ct.Committed())
	})

	t.Run("ReadFrom", func(t *testing.T) {
		for _, tee := range []bool{false, true} {
			for _, c := range []struct {
				name      string
				reader    io.Reader
				committed func(tee bool) bool
			}{
				{"empty", bytes.NewReader(nil), func(tee bool) bool { return !tee }},
				{"read error", errReader{}, func(tee bool) bool { return !tee }},
				{"data", bytes.NewReader([]byte("application-body")), func(bool) bool { return true }},
			} {
				tw, ct, ww, _ := full()
				if tee {
					ww.Tee(io.Discard)
				}
				_, _ = tw.(io.ReaderFrom).ReadFrom(c.reader)
				assert.Equal(t, c.committed(tee), ct.Committed(), "%s tee=%v", c.name, tee)
			}
		}
	})

	t.Run("Hijack", func(t *testing.T) {
		tw, ct, _, f := full()
		_, _, err := tw.(http.Hijacker).Hijack()
		require.NoError(t, err)
		assert.True(t, ct.Committed())
		assert.Equal(t, 0, ct.Status())
		assert.Equal(t, 1, f.hijacks)

		tw, ct, _, f = full()
		f.hijackErr = errors.New("cannot hijack")
		_, _, err = tw.(http.Hijacker).Hijack()
		require.Error(t, err)
		assert.False(t, ct.Committed())

		tw, ct, _, _ = full()
		_, _, err = http.NewResponseController(tw).Hijack()
		require.NoError(t, err)
		assert.True(t, ct.Committed())
	})

	t.Run("Push", func(t *testing.T) {
		f := newFakeRaw()
		tw, ct, _ := track(t, rawWith(f, rawFlusher|rawPusher), 2, nil)
		require.NoError(t, tw.(http.Pusher).Push("/x", nil))
		assert.False(t, ct.Committed())
	})

	t.Run("complete", func(t *testing.T) {
		for name, c := range map[string]struct {
			raw      func() http.ResponseWriter
			complete bool
		}{
			"plain":                    {func() http.ResponseWriter { return newFakeRaw() }, true},
			"hijacker behind Unwrap":   {func() http.ResponseWriter { return plain{hijackOnly{newFakeRaw()}} }, false},
			"15 plain wrappers":        {func() http.ResponseWriter { return wrapN(rawWith(newFakeRaw(), 0), 15) }, true},
			"16 plain wrappers":        {func() http.ResponseWriter { return wrapN(rawWith(newFakeRaw(), 0), 16) }, false},
			"17 plain wrappers":        {func() http.ResponseWriter { return wrapN(rawWith(newFakeRaw(), 0), 17) }, false},
			"Unwrap returns itself":    {func() http.ResponseWriter { return &cyclic{rawWith(newFakeRaw(), 0)} }, false},
			"opaque (no Unwrap)":       {func() http.ResponseWriter { return opaque{newFakeRaw()} }, true},
			"hijacker, chi can hijack": {func() http.ResponseWriter { return rawWith(newFakeRaw(), rawHijacker) }, true},
		} {
			_, ct, _ := track(t, c.raw(), 1, nil)
			assert.Equal(t, c.complete, ct.complete, name)
		}
	})
}

// wrapN wraps w n times in plain wrappers.
func wrapN(w http.ResponseWriter, n int) http.ResponseWriter {
	for i := 0; i < n; i++ {
		w = plain{w}
	}
	return w
}

// TestCommitTrackerParent checks how a tracker reads the state of an outer tracker.
func TestCommitTrackerParent(t *testing.T) {
	// outer returns an outer tracker with its context.
	outer := func() (http.ResponseWriter, *commitTracker, context.Context, *fakeRaw) {
		f := newFakeRaw()
		tw, ct, _ := track(t, rawWith(f, rawFlusher|rawHijacker), 1, nil)
		return tw, ct, context.WithValue(context.Background(), trackerCtxKey{}, ct), f
	}

	t.Run("through Unwrap", func(t *testing.T) {
		otw, oct, ctx, f := outer()
		_, ict, _ := track(t, plain{otw}, 1, ctx)
		require.Same(t, oct, ict.parent)
		assert.True(t, ict.complete)
		assert.False(t, ict.Committed())

		// The state is read live, also when the outer flush is after the creation.
		otw.(http.Flusher).Flush()
		assert.True(t, ict.Committed())
		assert.Equal(t, 200, ict.Status())
		assert.Equal(t, 1, f.flushes)

		// A flush before the creation.
		_, ict, _ = track(t, plain{otw}, 1, ctx)
		assert.True(t, ict.Committed())
		assert.Equal(t, 200, ict.Status())
	})

	t.Run("hijack of the outer writer", func(t *testing.T) {
		otw, _, ctx, _ := outer()
		_, ict, _ := track(t, plain{otw}, 1, ctx)
		_, _, err := otw.(http.Hijacker).Hijack()
		require.NoError(t, err)
		assert.True(t, ict.Committed())
		assert.Equal(t, 0, ict.Status())
	})

	t.Run("hidden parent", func(t *testing.T) {
		otw, _, ctx, _ := outer()
		// A wrapper without Unwrap() is between them: the tracer is in the context,
		// but the writer chain does not reach it.
		_, ict, _ := track(t, opaque{otw}, 1, ctx)
		assert.Nil(t, ict.parent)
		assert.False(t, ict.complete)
		otw.(http.Flusher).Flush()
		assert.False(t, ict.Committed(), "own state only")
		assert.Equal(t, 0, ict.Status())
	})

	t.Run("replaced writer", func(t *testing.T) {
		otw, _, ctx, _ := outer()
		_, ict, _ := track(t, newFakeRaw(), 1, ctx) // another writer, same context
		assert.Nil(t, ict.parent)
		assert.False(t, ict.complete)
		otw.WriteHeader(201)
		assert.Equal(t, 0, ict.Status())
		assert.False(t, ict.Committed())
	})

	t.Run("no outer tracer detectable", func(t *testing.T) {
		_, ict, _ := track(t, newFakeRaw(), 1, context.Background())
		assert.Nil(t, ict.parent)
		assert.True(t, ict.complete)
		// The same when a middleware replaced the context but kept the writer chain
		// opaque: it is not detected (documented limit).
		otw, _, _, _ := outer()
		_, ict, _ = track(t, opaque{otw}, 1, context.Background())
		assert.Nil(t, ict.parent)
		assert.True(t, ict.complete, "no context value: no detectable outer tracker")
	})

	t.Run("status", func(t *testing.T) {
		flusherPlain := func(w http.ResponseWriter) http.ResponseWriter {
			return struct {
				plain
				http.Flusher
			}{plain{w}, w.(http.Flusher)}
		}
		// The outer status wins, also over the status of the inner chi writer.
		otw, _, ctx, _ := outer()
		itw, ict, _ := track(t, flusherPlain(otw), 1, ctx)
		otw.WriteHeader(201)
		itw.(http.Flusher).Flush()
		assert.Equal(t, 201, ict.Status())

		otw, _, ctx, _ = outer()
		itw, ict, _ = track(t, plain{otw}, 1, ctx)
		otw.WriteHeader(201)
		_, _ = itw.Write([]byte("x")) // chi's inner status is 200
		assert.Equal(t, 201, ict.Status())

		otw, _, ctx, _ = outer()
		itw, ict, _ = track(t, plain{otw}, 1, ctx)
		otw.(http.Flusher).Flush()
		itw.WriteHeader(404)
		assert.Equal(t, 200, ict.Status())
	})

	t.Run("incomplete parent", func(t *testing.T) {
		f := newFakeRaw()
		otw, oct, _ := track(t, &cyclic{f}, 1, nil)
		require.False(t, oct.complete)
		ctx := context.WithValue(context.Background(), trackerCtxKey{}, oct)
		_, ict, _ := track(t, plain{otw}, 1, ctx)
		require.Same(t, oct, ict.parent)
		assert.False(t, ict.complete)
		// An incomplete tracker with a visible parent reads the state of the parent.
		otw.WriteHeader(202)
		assert.True(t, ict.Committed())
		assert.Equal(t, 202, ict.Status())
	})
}
