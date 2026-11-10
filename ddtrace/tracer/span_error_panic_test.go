// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io/fs"
	"testing"

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

func TestSpanFinishWithPanickingError(t *testing.T) {
	for name, newErr := range panickingErrors() {
		t.Run(name, func(t *testing.T) {
			tr, transport, flush, stop, err := startTestTracer(t)
			require.NoError(t, err)
			defer stop()

			parent := tr.StartSpan("parent")
			child := tr.StartSpan("child", ChildOf(parent.Context()))

			r := callRecover(func() { child.Finish(WithError(newErr())) })
			assert.Nil(t, r, "Finish must not panic")
			assertSpanUnlocked(t, child)

			if child.mu.TryLock() {
				assert.True(t, child.finished, "child span was not finished")
				child.mu.Unlock()
			}

			parent.Finish()
			flush(1) // fails the test if no trace is sent within the timeout
			traces := transport.Traces()
			require.Len(t, traces, 1)
			assert.Len(t, traces[0], 2)
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
			if s != nil {
				assertSpanUnlocked(t, s)
			}
		})
	}
}
