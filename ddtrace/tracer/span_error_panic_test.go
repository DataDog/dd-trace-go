// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for https://github.com/DataDog/dd-trace-go/issues/5812:
// an error whose Error method panics must not leave the span locked,
// unfinished, or drop the trace.

// nilPtrError is an error whose Error method dereferences its receiver, so a
// typed-nil *nilPtrError panics when Error is called.
type nilPtrError struct{ msg string }

func (e *nilPtrError) Error() string { return e.msg }

// panickingErrors returns errors whose Error method panics.
func panickingErrors() map[string]func() error {
	return map[string]func() error{
		// fs.PathError.Error calls e.Err.Error(); a nil Err panics.
		"fs.PathError with nil Err": func() error { return &fs.PathError{Op: "open", Path: "/x"} },
		// A typed-nil pointer stored in an error interface is a non-nil error.
		"typed-nil pointer": func() error { var e *nilPtrError; return e },
	}
}

// callRecover runs fn and returns the recovered panic value, if any.
func callRecover(fn func()) (r any) {
	defer func() { r = recover() }()
	fn()
	return nil
}

// assertSpanUnlocked checks the span mutex is free without blocking.
func assertSpanUnlocked(t *testing.T, s *Span) {
	t.Helper()
	if assert.True(t, s.mu.TryLock(), "span mutex is still held") {
		s.mu.Unlock()
	}
}

// newFakeAgent starts a local HTTP agent that counts the traces it receives,
// using the X-Datadog-Trace-Count header the tracer sets on each payload.
func newFakeAgent(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var traces atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/traces") {
			n, _ := strconv.Atoi(r.Header.Get("X-Datadog-Trace-Count"))
			traces.Add(int64(n))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, &traces
}

// waitFor runs fn in a goroutine and reports whether it returned within d.
func waitFor(fn func(), d time.Duration) bool {
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func TestSpanFinishWithPanickingError(t *testing.T) {
	for name, newErr := range panickingErrors() {
		t.Run(name, func(t *testing.T) {
			srv, received := newFakeAgent(t)
			require.NoError(t, Start(WithAgentURL(srv.URL), WithLogStartup(false)))

			parent := StartSpan("parent")
			child := StartSpan("child", ChildOf(parent.Context()))

			r := callRecover(func() { child.Finish(WithError(newErr())) })
			assert.Nil(t, r, "Finish must not panic")
			assertSpanUnlocked(t, child)

			if child.mu.TryLock() {
				assert.True(t, child.finished, "child span was not finished")
				child.mu.Unlock()
			}

			parent.Finish()
			// Stop flushes pending traces to the agent.
			require.True(t, waitFor(Stop, 5*time.Second), "tracer.Stop did not return")

			deadline := time.After(2 * time.Second)
			for received.Load() < 1 {
				select {
				case <-deadline:
					t.Fatalf("fake agent received %d traces, want 1", received.Load())
				case <-time.After(10 * time.Millisecond):
				}
			}
			assert.EqualValues(t, 1, received.Load())
		})
	}
}

// SetTag uses a deferred unlock, so it releases the lock even when Error
// panics. It still panics, which it should not.
func TestSpanSetTagWithPanickingError(t *testing.T) {
	for name, newErr := range panickingErrors() {
		t.Run(name, func(t *testing.T) {
			s := newBasicSpan("op")
			r := callRecover(func() { s.SetTag(ext.Error, newErr()) })
			assert.Nil(t, r, "SetTag must not panic")
			assertSpanUnlocked(t, s)
		})
	}
}

// StartSpan applies Tag options through setTags, which unlocks without defer.
func TestStartSpanTagWithPanickingError(t *testing.T) {
	for name, newErr := range panickingErrors() {
		t.Run(name, func(t *testing.T) {
			tr, _, _, stop, err := startTestTracer(t)
			require.NoError(t, err)
			defer stop()

			var s *Span
			r := callRecover(func() { s = tr.StartSpan("op", Tag(ext.Error, newErr())) })
			assert.Nil(t, r, "StartSpan must not panic")
			require.NotNil(t, s, "StartSpan did not return a span")
			assertSpanUnlocked(t, s)
		})
	}
}

// setTags is the helper StartSpan uses for Tag options. Calling it on an
// existing span makes the lock leak observable even though StartSpan itself
// never returns the span when it panics.
func TestSpanSetTagsWithPanickingError(t *testing.T) {
	for name, newErr := range panickingErrors() {
		t.Run(name, func(t *testing.T) {
			s := newBasicSpan("op")
			r := callRecover(func() { s.setTags(map[string]any{ext.Error: newErr()}) })
			assert.Nil(t, r, "setTags must not panic")
			assertSpanUnlocked(t, s)
		})
	}
}
