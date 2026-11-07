// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// The codes that the tests use besides those that this package names.
const (
	codeNotFound    Code = 5
	codeAborted     Code = 10
	codeUnavailable Code = 14
)

// testError is the *connect.Error of the connect major version that testLibrary describes. Like
// connect's, its methods dereference their receiver.
type testError struct {
	code        Code
	message     string
	cause       error
	remote      bool
	notModified bool
	details     []*testDetail
}

// testDetail is an error detail of the fake connect major version. Like connect's, decoding it
// dereferences it.
type testDetail struct {
	// message is the decoded detail; nil stands for a detail that can't be decoded.
	message proto.Message
}

var errUndecodable = errors.New("undecodable detail")

func decodeTestDetail(detail *testDetail) (proto.Message, error) {
	if detail.message == nil {
		return nil, errUndecodable
	}
	return detail.message, nil
}

func newTestError(code Code, message string) *testError {
	return &testError{code: code, message: message}
}

func (e *testError) Error() string {
	if e.message == "" {
		return e.code.String()
	}
	return e.code.String() + ": " + e.message
}

func (e *testError) Unwrap() error { return e.cause }

func (e *testError) withCause(cause error) *testError {
	clone := *e
	clone.cause = cause
	return &clone
}

func (e *testError) withRemote() *testError {
	clone := *e
	clone.remote = true
	return &clone
}

func (e *testError) withDetails(details ...*testDetail) *testError {
	clone := *e
	clone.details = details
	return &clone
}

// notModifiedError returns the not-modified error of the fake connect major version.
func notModifiedError() *testError {
	return &testError{code: CodeUnknown, message: "not modified", notModified: true}
}

// errorWithDetails returns an error with one detail, which error detail tags show as "seconds:1".
func errorWithDetails(code Code) *testError {
	return newTestError(code, "internal error").withDetails(&testDetail{message: &durationpb.Duration{Seconds: 1}})
}

// testLibrary describes the errors of a fake connect major version: *testError.
var testLibrary = Library{
	InspectError: func(err error) ErrorInfo {
		testErr, ok := errors.AsType[*testError](err)
		switch {
		case !ok:
			return ErrorInfo{}
		case testErr == nil:
			return ErrorInfo{Coded: true, Nil: true}
		default:
			return ErrorInfo{Coded: true, Code: testErr.code, Remote: testErr.remote}
		}
	},
	IsNotModified: func(err error) bool {
		testErr, ok := errors.AsType[*testError](err)
		return ok && testErr != nil && testErr.notModified
	},
	ErrorDetails: func(err error, tags ErrorDetailTags) {
		if testErr, ok := errors.AsType[*testError](err); ok && testErr != nil {
			TagErrorDetails(tags, testErr.details, decodeTestDetail)
		}
	},
}

// nilTestErrorWrapper wraps a nil *testError without calling any of its methods.
type nilTestErrorWrapper struct{}

func (nilTestErrorWrapper) Error() string { return "wrapped nil connect error" }
func (nilTestErrorWrapper) Unwrap() error { return (*testError)(nil) }

// derefError dereferences its receiver in every method, like a typed-nil custom error would.
type derefError struct{ cause error }

func (e *derefError) Error() string { return e.cause.Error() }
func (e *derefError) Unwrap() error { return e.cause }

// derefIsError dereferences its receiver only in its Is method.
type derefIsError struct{ target error }

func (*derefIsError) Error() string          { return "deref is" }
func (e *derefIsError) Is(target error) bool { return target == e.target }

// wrappedDerefError and wrappedDerefIsError have a typed-nil error in their chain, so walking the
// chain panics.
var (
	wrappedDerefError   = fmt.Errorf("op: %w", (*derefError)(nil))
	wrappedDerefIsError = fmt.Errorf("op: %w", (*derefIsError)(nil))
)

// formatPanicError's Error method panics with value, as an fs.PathError's does when its Err is nil.
// Walking its chain, cause included, does not panic.
type formatPanicError struct {
	value any
	cause error
}

func (e *formatPanicError) Error() string { panic(e.value) }
func (e *formatPanicError) Unwrap() error { return e.cause }

// probeCounter counts how often a chain that holds it is walked to check that it is safe to
// inspect.
type probeCounter struct{ probes int }

func (*probeCounter) Error() string { return "probe counter" }

func (c *probeCounter) Is(target error) bool {
	if target == errInspectionProbe {
		c.probes++
	}
	return false
}

// pointerError, valueError and funcError are errors of the dynamic types that isNilError tells
// apart.
type (
	pointerError struct{}
	valueError   struct{}
	funcError    func() string
)

func (*pointerError) Error() string { return "pointer" }
func (valueError) Error() string    { return "value" }
func (f funcError) Error() string   { return f() }

func TestIsNilError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "typed nil pointer", err: (*pointerError)(nil), want: true},
		{name: "pointer", err: new(pointerError), want: false},
		{name: "value", err: valueError{}, want: false},
		{name: "typed nil func", err: funcError(nil), want: true},
		{name: "func", err: funcError(func() string { return "func" }), want: false},
		{name: "wrapped typed nil pointer", err: errors.Join((*pointerError)(nil)), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, isNilError(test.err))
		})
	}
}

func TestCodeOf(t *testing.T) {
	remote := newTestError(codeNotFound, "upstream").withRemote()
	osDeadline := fmt.Errorf("query: %w", os.ErrDeadlineExceeded)
	for _, test := range []struct {
		name string
		err  error
		want Code
		// wantWire is the code that the server transports send for err, and wantHTTP the one that
		// connect's HTTP server transports send, if it differs.
		wantWire, wantHTTP Code
	}{
		{name: "connect error", err: newTestError(codeNotFound, "nope"), want: codeNotFound, wantWire: codeNotFound},
		{
			name: "explicit code beats a cancellation", err: newTestError(CodeInternal, "canceled").withCause(context.Canceled),
			want: CodeInternal, wantWire: CodeInternal,
		},
		{name: "cancellation", err: fmt.Errorf("call: %w", context.Canceled), want: CodeCanceled, wantWire: CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: CodeDeadlineExceeded, wantWire: CodeDeadlineExceeded},
		{name: "other error", err: errors.New("boom"), want: CodeUnknown, wantWire: CodeUnknown},
		{name: "typed nil connect error", err: (*testError)(nil), want: CodeUnknown, wantWire: CodeUnknown},
		{name: "nil connect error in the chain", err: nilTestErrorWrapper{}, want: CodeUnknown, wantWire: CodeUnknown},
		{name: "remote error", err: remote, want: codeNotFound, wantWire: CodeInternal},
		{name: "wrapped remote error", err: fmt.Errorf("call: %w", remote), want: codeNotFound, wantWire: CodeInternal},
		{
			name: "local error caused by a remote error", err: newTestError(codeUnavailable, "retry").withCause(remote),
			want: codeUnavailable, wantWire: codeUnavailable,
		},
		{name: "uncoded os deadline", err: osDeadline, want: CodeUnknown, wantWire: CodeUnknown, wantHTTP: CodeDeadlineExceeded},
		{
			name: "network timeout", err: &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
			want: CodeUnknown, wantWire: CodeUnknown, wantHTTP: CodeDeadlineExceeded,
		},
		{name: "coded os deadline", err: newTestError(codeUnavailable, "retry").withCause(osDeadline), want: codeUnavailable, wantWire: codeUnavailable},
		{name: "unknown code beats an os deadline", err: newTestError(CodeUnknown, "query").withCause(osDeadline), want: CodeUnknown, wantWire: CodeUnknown},
		{name: "cancellation beats an os deadline", err: errors.Join(osDeadline, context.Canceled), want: CodeCanceled, wantWire: CodeCanceled},
		{name: "remote error caused by an os deadline", err: newTestError(CodeUnknown, "upstream").withCause(osDeadline).withRemote(), want: CodeUnknown, wantWire: CodeInternal},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, testLibrary.codeOf(test.err))
			assert.Equal(t, test.wantWire, testLibrary.wireCodeOf(test.err, false))
			assert.Equal(t, cmp.Or(test.wantHTTP, test.wantWire), testLibrary.wireCodeOf(test.err, true))
		})
	}
}

func TestIsExpectedStreamEOF(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "EOF", err: io.EOF, want: true},
		{name: "wrapped EOF", err: fmt.Errorf("read: %w", io.EOF), want: true},
		{name: "connect end-of-stream sentinel", err: newTestError(CodeUnknown, "EOF").withCause(io.EOF), want: true},
		{name: "coded EOF", err: newTestError(CodeInternal, "EOF").withCause(io.EOF)},
		{name: "remote error caused by EOF", err: newTestError(CodeUnknown, "").withCause(io.EOF).withRemote()},
		{name: "wrapped remote error caused by EOF", err: fmt.Errorf("call: %w", newTestError(CodeUnknown, "").withCause(io.EOF).withRemote())},
		{name: "other error", err: errors.New("boom")},
		{name: "cancellation", err: context.Canceled},
		{name: "typed nil connect error", err: (*testError)(nil)},
		{name: "nil connect error in the chain", err: nilTestErrorWrapper{}},
		{name: "typed nil custom error", err: (*derefError)(nil)},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Callers pass errors that are safe to inspect.
			err := testLibrary.normalizeError(test.err)
			var got bool
			require.NotPanics(t, func() { got = testLibrary.isExpectedStreamEOF(err) })
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.want, newTestConfig().IsExpectedStreamEOF(err))
		})
	}

	t.Run("a normalized error is not checked again", func(t *testing.T) {
		counter := new(probeCounter)
		err := testLibrary.normalizeError(errors.Join(io.EOF, counter))
		require.Equal(t, 1, counter.probes)
		assert.True(t, testLibrary.isExpectedStreamEOF(err))
		assert.True(t, newTestConfig().IsExpectedStreamEOF(err))
		assert.Equal(t, 1, counter.probes)
	})
}

func TestIsRemoteError(t *testing.T) {
	remote := newTestError(codeNotFound, "upstream").withRemote()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "remote error", err: remote, want: true},
		{name: "wrapped remote error", err: fmt.Errorf("call: %w", remote), want: true},
		{name: "local error", err: newTestError(codeNotFound, "nope")},
		{name: "local error caused by a remote error", err: newTestError(codeUnavailable, "retry").withCause(remote)},
		{name: "other error", err: errors.New("boom")},
		{name: "typed nil connect error", err: (*testError)(nil)},
		{name: "nil connect error in the chain", err: nilTestErrorWrapper{}},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got bool
			require.NotPanics(t, func() { got = newTestConfig().IsRemoteError(test.err) })
			assert.Equal(t, test.want, got)
		})
	}
}

func TestNormalizeError(t *testing.T) {
	boom := errors.New("boom")
	for _, test := range []struct {
		name     string
		err      error
		wantSame bool
	}{
		{name: "nil", err: nil, wantSame: true},
		{name: "error", err: boom, wantSame: true},
		{name: "connect error", err: newTestError(CodeInternal, "boom").withCause(boom), wantSame: true},
		{name: "typed nil connect error", err: (*testError)(nil)},
		{name: "typed nil custom error", err: (*derefError)(nil)},
		{name: "nil connect error in the chain", err: nilTestErrorWrapper{}},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
		{name: "wrapped typed nil error whose Is method panics", err: wrappedDerefIsError},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got error
			require.NotPanics(t, func() { got = testLibrary.normalizeError(test.err) })
			if test.wantSame {
				assert.Equal(t, test.err, got)
				return
			}
			require.Error(t, got)
			assert.True(t, testLibrary.isSafeToInspect(got))
			assert.Contains(t, got.Error(), fmt.Sprintf("%T", test.err))
		})
	}
}

func TestPanicError(t *testing.T) {
	boom := errors.New("boom")
	for _, test := range []struct {
		name    string
		value   any
		check   func(*testing.T, error)
		wantMsg string
	}{
		{name: "error", value: boom, check: func(t *testing.T, err error) { assert.ErrorIs(t, err, boom) }},
		{name: "string", value: "boom", wantMsg: "panic: boom"},
		{name: "typed nil connect error", value: (*testError)(nil), wantMsg: "panic"},
		{name: "nil connect error in the chain", value: nilTestErrorWrapper{}, wantMsg: "unsafe to inspect"},
		{name: "wrapped typed nil custom error", value: wrappedDerefError, wantMsg: "unsafe to inspect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = testLibrary.panicError(test.value) })
			require.Error(t, err)
			require.NotPanics(t, func() { _ = err.Error() })
			if test.check != nil {
				test.check(t, err)
			}
			assert.Contains(t, err.Error(), test.wantMsg)
		})
	}
}

// TestLibraryDefaults checks what a Library without functions reports: no error holds a
// *connect.Error, is a not-modified error or has details.
func TestLibraryDefaults(t *testing.T) {
	var lib Library
	coded := errorWithDetails(codeNotFound)
	assert.Equal(t, ErrorInfo{}, lib.inspect(coded))
	assert.Equal(t, CodeUnknown, lib.codeOf(coded))
	assert.Equal(t, CodeCanceled, lib.codeOf(newTestError(CodeInternal, "canceled").withCause(context.Canceled)))
	assert.True(t, lib.isUncodedCancellation(newTestError(CodeInternal, "canceled").withCause(context.Canceled)))
	assert.False(t, lib.isNotModified(notModifiedError()))
	assert.True(t, lib.isExpectedStreamEOF(newTestError(CodeInternal, "EOF").withCause(io.EOF)))

	mt := startMockTracer(t)
	cfg := newTestConfig((*Config).EnableErrorDetailTags)
	cfg.lib = &lib
	cfg.FinishSpan(tracer.StartSpan("finish"), coded, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "unknown", spans[0].Tag(tagConnectErrorCode))
	assert.Empty(t, tagsWithPrefix(spans[0], connectProtocol.statusDetailsPrefix))
}
