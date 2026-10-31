// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
)

// ErrorInfo is what a connect major version reports about an error chain.
type ErrorInfo struct {
	// Coded reports whether the chain holds a *connect.Error.
	Coded bool
	// Nil reports whether that *connect.Error is a nil pointer.
	Nil bool
	// Code is the code that its Code method reports. It is not used when Nil is set.
	Code Code
	// Remote reports whether that *connect.Error is a peer's verdict, which server transports
	// don't forward as the handler's own.
	Remote bool
}

// Library describes the errors of a connect major version. An integration declares it once. Its
// functions are only called with errors that are safe to inspect, or within a recover; a nil
// function reports an error that holds no *connect.Error, is not a not-modified error, and has no
// details.
type Library struct {
	// InspectError reports on err's chain.
	InspectError func(err error) ErrorInfo
	// IsNotModified reports whether err is connect's not-modified error.
	IsNotModified func(err error) bool
	// ErrorDetails tags, with TagErrorDetails, the details of the *connect.Error in err's chain.
	ErrorDetails func(err error, tags ErrorDetailTags)
}

func (l *Library) inspect(err error) ErrorInfo {
	if l.InspectError == nil {
		return ErrorInfo{}
	}
	return l.InspectError(err)
}

func (l *Library) isNotModified(err error) bool {
	return l.IsNotModified != nil && l.IsNotModified(err)
}

// codeOf returns the code of err: an explicit connect code wins over the context error it wraps.
func (l *Library) codeOf(err error) Code {
	if info := l.inspect(err); info.Coded {
		if info.Nil {
			return CodeUnknown
		}
		return info.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadlineExceeded
	default:
		return CodeUnknown
	}
}

// wireCodeOf is codeOf as the server transports report a handler's error: they don't forward a
// remote *connect.Error, the verdict of a call the handler made, as its own, but send
// CodeInternal. Over HTTP, connect also sends an uncoded error that wraps os.ErrDeadlineExceeded,
// unless it wraps context.Canceled, as CodeDeadlineExceeded.
func (l *Library) wireCodeOf(err error, overHTTP bool) Code {
	info := l.inspect(err)
	if info.Coded && info.Remote {
		return CodeInternal
	}
	code := l.codeOf(err)
	if overHTTP && !info.Coded && code == CodeUnknown && errors.Is(err, os.ErrDeadlineExceeded) {
		return CodeDeadlineExceeded
	}
	return code
}

// isExpectedStreamEOF reports whether err, which must be safe to inspect (see normalizeError), is
// connect's normal end-of-stream signal: io.EOF, which a transport may wrap with CodeUnknown. An
// EOF with any other explicit code is a real error, and so is a remote error: an in-process
// transport may keep the error that the handler returned, possibly an io.EOF, as the cause of the
// peer's verdict.
func (l *Library) isExpectedStreamEOF(err error) bool {
	if err == nil {
		return false
	}
	if info := l.inspect(err); info.Coded && info.Remote {
		return false
	}
	return l.codeOf(err) == CodeUnknown && errors.Is(err, io.EOF)
}

// isUncodedCancellation reports whether err is a context cancellation without an explicit
// connect code, which is never recorded as an error.
func (l *Library) isUncodedCancellation(err error) bool {
	if l.inspect(err).Coded {
		return false
	}
	return errors.Is(err, context.Canceled)
}

// hasNilConnectError reports whether err's chain contains a nil *connect.Error.
func (l *Library) hasNilConnectError(err error) bool {
	info := l.inspect(err)
	return info.Coded && info.Nil
}

// normalizeError replaces an error that is unsafe to inspect with one that is safe. The methods of
// a nil error, or of a nil *connect.Error in its chain, may panic, and so may walking a chain whose
// Unwrap, Is or As methods dereference a nil receiver.
func (l *Library) normalizeError(err error) error {
	if err == nil || l.isSafeToInspect(err) {
		return err
	}
	return fmt.Errorf("connect: error value (%T) is unsafe to inspect: it is nil, has a nil *connectrpc.Error in its chain, or panics when unwrapped", err)
}

func (l *Library) isSafeToInspect(err error) (safe bool) {
	defer func() {
		if recover() != nil {
			safe = false
		}
	}()
	if isNilError(err) || l.hasNilConnectError(err) {
		return false
	}
	// A target that never matches makes errors.Is call every Unwrap and Is method in the chain.
	_ = errors.Is(err, errInspectionProbe)
	return true
}

var errInspectionProbe = errors.New("connect: error inspection probe")

// recordableError returns err or, if its Error method panics, a substitute that names its type: the
// tracer calls Error while it holds the span's lock, which the panic would leave locked.
func recordableError(err error) error {
	if errorPanics(err) {
		return fmt.Errorf("connect: error value (%T) panics when formatted", err)
	}
	return err
}

// errorPanics reports whether err's Error method panics. The result stays set unless Error returns,
// so that a panic(nil), which recover reports as nil with GODEBUG=panicnil=1, counts too.
func errorPanics(err error) (panics bool) {
	panics = true
	defer func() { _ = recover() }()
	_ = err.Error()
	return false
}

// panicError returns the error that spans record for a recovered panic value.
func (l *Library) panicError(value any) error {
	if err, ok := value.(error); ok && !isNilError(err) {
		return l.normalizeError(err)
	}
	return fmt.Errorf("panic: %v", value)
}

// isNilError reports whether err is a non-nil interface holding a nil value.
func isNilError(err error) bool {
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}
