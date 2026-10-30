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
	"maps"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
)

func nonErrorCodes(codes ...Code) func(*Config) {
	return func(c *Config) { SetNonErrorCodes(c, codes) }
}

func errorCheck(fn func(procedure string, err error) bool) func(*Config) {
	return func(c *Config) { c.SetErrorCheck(fn) }
}

// requireFinished returns the only finished span.
func requireFinished(t *testing.T, mt mocktracer.Tracer) *mocktracer.Span {
	t.Helper()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func TestFinishSpan(t *testing.T) {
	var (
		typedNilConnectErr *testError
		call               = FinishMode{}
		serverCall         = FinishMode{Wire: true}
		httpServerCall     = FinishMode{Wire: true, HTTP: true}
		message            = FinishMode{AllowEOF: true}
		// unaryGET is the mode of a unary call over Connect with GET, which may be answered with a 304.
		unaryGET = UnaryFinishMode(&connectProtocol, http.MethodGet, false)
		boom     = newTestError(CodeInternal, "boom")
		remote   = newTestError(codeNotFound, "upstream").withRemote()
		grpcWeb  = ProtocolFor("grpcweb")
		// unknown is the protocol of a call that no transport reported one for.
		unknown = ProtocolFor("")
		// formatPanics' Error method panics. The tracer calls it while it holds the span's lock.
		formatPanics = &formatPanicError{value: "Error called"}
		osDeadline   = fmt.Errorf("query: %w", os.ErrDeadlineExceeded)
	)
	for _, test := range []struct {
		name     string
		err      error
		protocol *Protocol // connectProtocol if nil
		mode     FinishMode
		panic    bool
		setters  []func(*Config)
		// wantCode is the protocol's status tag, or nil if the tag must be absent.
		wantCode any
		// wantError is a substring of the recorded error; empty if no error may be recorded.
		wantError string
		want304   bool
		wantTags  map[string]any // a nil value means the tag is absent
	}{
		{name: "nil error", mode: call},
		{name: "nil error over gRPC", protocol: &grpcProtocol, mode: call, wantCode: 0},
		{name: "nil error without a protocol", protocol: &inProcessProtocol, mode: call},
		{name: "error", err: boom, mode: call, wantCode: "internal", wantError: "boom"},
		{name: "error over gRPC", err: boom, protocol: &grpcProtocol, mode: call, wantCode: uint32(CodeInternal), wantError: "boom"},
		{name: "error over gRPC-Web", err: boom, protocol: grpcWeb, mode: call, wantCode: uint32(CodeInternal), wantError: "boom"},
		{name: "error over an unknown protocol", err: boom, protocol: ProtocolFor("future-protocol"), mode: call, wantError: "boom"},
		{name: "error over an unreported protocol", err: boom, protocol: unknown, mode: call, wantError: "boom"},
		{name: "nil error over an unreported protocol", protocol: unknown, mode: call},
		{name: "panic over an unreported protocol", err: boom, protocol: unknown, mode: call, panic: true, wantError: "boom"},
		{
			name: "not modified over an unreported protocol", err: notModifiedError(), protocol: unknown,
			mode: UnaryFinishMode(unknown, http.MethodGet, false), wantError: "not modified",
		},
		{
			name: "error details over an unreported protocol", err: errorWithDetails(CodeInternal), protocol: unknown, mode: call,
			setters: []func(*Config){(*Config).EnableErrorDetailTags}, wantError: "internal error",
			wantTags: map[string]any{"connect.status_details._0": "seconds:1"},
		},
		{name: "error without a protocol", err: boom, protocol: &inProcessProtocol, mode: call, wantCode: "internal", wantError: "boom"},
		{
			name: "error stack", err: boom, mode: call, wantCode: "internal", wantError: "boom",
			wantTags: map[string]any{ext.ErrorHandlingStack: assert.ValueAssertionFunc(assert.NotNil)},
		},
		{
			name: "NoDebugStack", err: boom, mode: call, setters: []func(*Config){(*Config).SetNoDebugStack}, wantCode: "internal", wantError: "boom",
			wantTags: map[string]any{ext.ErrorHandlingStack: nil, ext.ErrorStack: nil},
		},

		// End of stream. An in-process transport may return a plain io.EOF; HTTP ones wrap it.
		{name: "EOF ends a message stream", err: io.EOF, mode: message},
		{name: "EOF ends a gRPC message stream", err: io.EOF, protocol: &grpcProtocol, mode: message, wantCode: 0},
		{name: "connect end-of-stream sentinel", err: newTestError(CodeUnknown, "EOF").withCause(io.EOF), mode: message},
		{name: "wrapped EOF ends a message stream", err: fmt.Errorf("read: %w", io.EOF), mode: message},
		{name: "coded EOF is a message error", err: newTestError(CodeInternal, "EOF").withCause(io.EOF), mode: message, wantCode: "internal", wantError: "EOF"},
		{
			// An in-process transport may keep a handler's io.EOF as the cause of the client's error.
			name: "remote error caused by EOF is a message error", err: newTestError(CodeUnknown, "").withCause(io.EOF).withRemote(),
			protocol: &inProcessProtocol, mode: message, wantCode: "unknown", wantError: "unknown",
		},
		{name: "EOF is a call error", err: io.EOF, mode: call, wantCode: "unknown", wantError: "EOF"},

		// Not modified.
		{name: "not modified GET", err: notModifiedError(), mode: unaryGET, want304: true},
		{
			name: "not modified POST", err: notModifiedError(), mode: UnaryFinishMode(&connectProtocol, http.MethodPost, false),
			wantCode: "unknown", wantError: "not modified",
		},
		{
			name: "not modified over gRPC", err: notModifiedError(), protocol: &grpcProtocol,
			mode: UnaryFinishMode(&grpcProtocol, http.MethodGet, false), wantCode: uint32(CodeUnknown), wantError: "not modified",
		},
		{
			name: "not modified over gRPC-Web", err: notModifiedError(), protocol: grpcWeb,
			mode: UnaryFinishMode(grpcWeb, http.MethodGet, false), wantCode: uint32(CodeUnknown), wantError: "not modified",
		},
		{
			// An in-process transport may keep the handler's not-modified error.
			name: "not modified without a protocol", err: notModifiedError(), protocol: &inProcessProtocol,
			mode: UnaryFinishMode(&inProcessProtocol, http.MethodGet, false), wantCode: "unknown", wantError: "not modified",
		},
		{
			// A server reached without an HTTP transport has no HTTP method.
			name: "not modified on a server outside of connecthttp", err: notModifiedError(),
			mode: UnaryFinishMode(&connectProtocol, "", true), wantCode: "unknown", wantError: "not modified",
		},

		// Suppression.
		{name: "uncoded cancellation is never an error", err: context.Canceled, mode: call, setters: []func(*Config){nonErrorCodes()}, wantCode: "canceled"},
		{name: "uncoded cancellation ends a message stream", err: context.Canceled, protocol: &inProcessProtocol, mode: message, wantCode: "canceled"},
		{name: "uncoded deadline is an error", err: context.DeadlineExceeded, protocol: &inProcessProtocol, mode: call, wantCode: "deadline_exceeded", wantError: "deadline exceeded"},
		{name: "coded cancellation is suppressed by default", err: newTestError(CodeCanceled, "canceled").withCause(context.Canceled), mode: call, wantCode: "canceled"},
		{
			name: "coded cancellation can be an error", err: newTestError(CodeCanceled, "canceled").withCause(context.Canceled), mode: call,
			setters: []func(*Config){nonErrorCodes()}, wantCode: "canceled", wantError: "canceled",
		},
		{
			name: "explicit code beats the cause", err: newTestError(CodeInternal, "canceled").withCause(context.Canceled), mode: call,
			wantCode: "internal", wantError: "canceled",
		},
		{
			name: "NonErrorCodes keeps the status", err: newTestError(codeNotFound, "nope"), mode: call,
			setters: []func(*Config){nonErrorCodes(codeNotFound)}, wantCode: "not_found",
		},
		{
			name: "error check can suppress an error", err: boom, mode: call, wantCode: "internal",
			setters: []func(*Config){errorCheck(func(procedure string, err error) bool { return procedure != unaryProcedure || !errors.Is(err, boom) })},
		},

		// Remote errors: servers classify them with the code that the transports send.
		{name: "remote error on a client", err: remote, mode: call, wantCode: "not_found", wantError: "upstream"},
		{name: "remote error on a server", err: remote, mode: serverCall, wantCode: "internal", wantError: "upstream"},
		{name: "remote error on a gRPC server", err: remote, protocol: &grpcProtocol, mode: serverCall, wantCode: uint32(CodeInternal), wantError: "upstream"},
		{name: "wrapped remote error on a server", err: fmt.Errorf("call: %w", remote), mode: serverCall, wantCode: "internal", wantError: "upstream"},
		{
			name: "local error caused by a remote error on a server", err: newTestError(codeUnavailable, "retry").withCause(remote),
			mode: serverCall, wantCode: "unavailable", wantError: "retry",
		},
		{name: "local error on a server", err: boom, mode: serverCall, wantCode: "internal", wantError: "boom"},
		{
			name: "NonErrorCodes sees the code of a remote error on a server", err: remote, mode: serverCall,
			setters: []func(*Config){nonErrorCodes(CodeInternal)}, wantCode: "internal",
		},
		{
			name: "NonErrorCodes ignores the remote code on a server", err: remote, mode: serverCall,
			setters: []func(*Config){nonErrorCodes(codeNotFound)}, wantCode: "internal", wantError: "upstream",
		},
		{
			name: "error check gets the remote error on a server", err: remote, mode: serverCall, wantCode: "internal",
			setters: []func(*Config){errorCheck(func(_ string, err error) bool { return testLibrary.codeOf(err) != codeNotFound })},
		},
		{name: "panic with a remote error on a server", err: remote, mode: serverCall, panic: true, wantCode: "internal", wantError: "upstream"},

		// Uncoded errors that wrap os.ErrDeadlineExceeded: only connect's HTTP server transports send
		// them as CodeDeadlineExceeded.
		{name: "os deadline on an HTTP server", err: osDeadline, mode: httpServerCall, wantCode: "deadline_exceeded", wantError: "i/o timeout"},
		{
			name: "os deadline on a gRPC server", err: osDeadline, protocol: &grpcProtocol, mode: httpServerCall,
			wantCode: uint32(CodeDeadlineExceeded), wantError: "i/o timeout",
		},
		{name: "os deadline on a server without HTTP", err: osDeadline, protocol: &inProcessProtocol, mode: serverCall, wantCode: "unknown", wantError: "i/o timeout"},
		{name: "os deadline on a client", err: osDeadline, mode: call, wantCode: "unknown", wantError: "i/o timeout"},
		{
			name: "NonErrorCodes sees the code sent for an os deadline", err: osDeadline, mode: httpServerCall,
			setters: []func(*Config){nonErrorCodes(CodeDeadlineExceeded)}, wantCode: "deadline_exceeded",
		},
		{
			name: "explicit code beats an os deadline on an HTTP server", err: newTestError(codeUnavailable, "retry").withCause(osDeadline),
			mode: httpServerCall, wantCode: "unavailable", wantError: "retry",
		},
		{name: "cancellation beats an os deadline on an HTTP server", err: errors.Join(osDeadline, context.Canceled), mode: httpServerCall, wantCode: "canceled"},

		// Panics are always recorded.
		{name: "panic with a cancellation", err: context.Canceled, mode: call, panic: true, wantCode: "canceled", wantError: "canceled"},
		{
			name: "panic with a suppressed code", err: newTestError(CodeCanceled, "canceled").withCause(context.Canceled), mode: call, panic: true,
			wantCode: "canceled", wantError: "canceled",
		},
		{name: "panic with not modified", err: notModifiedError(), mode: unaryGET, panic: true, wantCode: "unknown", wantError: "not modified"},
		{name: "panic in a message span", err: context.Canceled, mode: message, panic: true, wantCode: "canceled", wantError: "canceled"},
		{name: "panic with EOF in a message span", err: io.EOF, mode: message, panic: true, wantCode: "unknown", wantError: "EOF"},
		// The server transports send a handler's panic as CodeInternal.
		{name: "panic on a server", err: newTestError(codeNotFound, "boom"), mode: serverCall, panic: true, wantCode: "internal", wantError: "boom"},
		{name: "panic with a value on a server", err: testLibrary.panicError("boom"), mode: serverCall, panic: true, wantCode: "internal", wantError: "panic: boom"},
		{
			name: "panic on a gRPC server", err: testLibrary.panicError("boom"), protocol: &grpcProtocol, mode: httpServerCall, panic: true,
			wantCode: uint32(CodeInternal), wantError: "panic: boom",
		},
		{name: "panic with a cancellation on a server", err: context.Canceled, mode: serverCall, panic: true, wantCode: "internal", wantError: "canceled"},
		{name: "panic with an os deadline on an HTTP server", err: osDeadline, mode: httpServerCall, panic: true, wantCode: "internal", wantError: "i/o timeout"},
		{
			name: "panic bypasses NonErrorCodes and the error check", err: newTestError(codeNotFound, "boom"), mode: call, panic: true,
			setters:  []func(*Config){nonErrorCodes(codeNotFound), errorCheck(func(string, error) bool { return false })},
			wantCode: "not_found", wantError: "boom",
		},

		// Errors that panic when inspected.
		{name: "typed nil connect error", err: typedNilConnectErr, mode: call, wantCode: "unknown", wantError: "unsafe to inspect"},
		{name: "typed nil custom error", err: (*derefError)(nil), mode: call, wantCode: "unknown", wantError: "unsafe to inspect"},
		{
			name: "nil connect error in the chain", err: nilTestErrorWrapper{}, mode: unaryGET, setters: []func(*Config){(*Config).EnableErrorDetailTags},
			wantCode: "unknown", wantError: "unsafe to inspect",
		},
		{name: "typed nil panic value", err: testLibrary.panicError(typedNilConnectErr), mode: call, panic: true, wantCode: "unknown", wantError: "panic"},
		{name: "typed nil message error", err: typedNilConnectErr, mode: message, wantCode: "unknown", wantError: "unsafe to inspect"},
		{name: "wrapped typed nil custom error", err: wrappedDerefError, mode: message, wantCode: "unknown", wantError: "unsafe to inspect"},
		{name: "typed nil server error", err: typedNilConnectErr, mode: serverCall, wantCode: "unknown", wantError: "unsafe to inspect"},

		// Errors whose Error method panics are recorded as a substitute, and classified as they are.
		{
			name: "error whose Error method panics", err: formatPanics, mode: call, wantCode: "unknown",
			wantError: "connect: error value (*connectshared.formatPanicError) panics when formatted",
		},
		{
			name: "coded error whose Error method panics", err: &formatPanicError{value: "Error called", cause: boom}, mode: serverCall,
			wantCode: "internal", wantError: "panics when formatted",
		},
		{
			name: "NoDebugStack with an error whose Error method panics", err: formatPanics, mode: message, setters: []func(*Config){(*Config).SetNoDebugStack},
			wantCode: "unknown", wantError: "panics when formatted", wantTags: map[string]any{ext.ErrorHandlingStack: nil},
		},
		{name: "panic with an error whose Error method panics", err: formatPanics, mode: call, panic: true, wantCode: "unknown", wantError: "panics when formatted"},
		{
			name: "error details of an error whose Error method panics", err: &formatPanicError{value: "Error called", cause: errorWithDetails(CodeInternal)},
			mode: call, setters: []func(*Config){(*Config).EnableErrorDetailTags}, wantCode: "internal", wantError: "panics when formatted",
			wantTags: map[string]any{"connect.status_details._0": "seconds:1"},
		},

		// Error details.
		{
			name: "error details", err: errorWithDetails(CodeInternal), mode: call, setters: []func(*Config){(*Config).EnableErrorDetailTags},
			wantCode: "internal", wantError: "internal error", wantTags: map[string]any{"connect.status_details._0": "seconds:1"},
		},
		{
			name: "gRPC error details", err: errorWithDetails(CodeInternal), protocol: &grpcProtocol, mode: call,
			setters: []func(*Config){(*Config).EnableErrorDetailTags}, wantCode: uint32(CodeInternal), wantError: "internal error",
			wantTags: map[string]any{"grpc.status_details._0": "seconds:1"},
		},
		{
			name: "no error details for a suppressed error", err: errorWithDetails(CodeInternal), mode: call,
			setters: []func(*Config){(*Config).EnableErrorDetailTags, nonErrorCodes(CodeInternal)}, wantCode: "internal",
			wantTags: map[string]any{"connect.status_details._0": nil},
		},
		{
			name: "no error details by default", err: errorWithDetails(CodeInternal), mode: call,
			wantCode: "internal", wantError: "internal error", wantTags: map[string]any{"connect.status_details._0": nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			protocol := test.protocol
			if protocol == nil {
				protocol = &connectProtocol
			}
			cfg := newTestConfig(test.setters...)
			f := SpanFinish{Procedure: unaryProcedure, Protocol: protocol, Mode: test.mode}
			span := tracer.StartSpan("finish")
			require.NotPanics(t, func() {
				if test.panic {
					cfg.finishSpan(span, test.err, f, true)
				} else {
					cfg.FinishSpan(span, test.err, f)
				}
			})
			finished := requireFinished(t, mt)
			for _, statusCodeTag := range []string{connectProtocol.statusCodeTag, grpcProtocol.statusCodeTag} {
				var want any
				if statusCodeTag == protocol.statusCodeTag {
					want = test.wantCode
				}
				assert.EqualValues(t, want, finished.Tag(statusCodeTag), statusCodeTag)
			}
			if test.wantError == "" {
				assert.Nil(t, finished.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, finished.Tag(ext.ErrorMsg), test.wantError)
			}
			if test.want304 {
				assert.Equal(t, "304", finished.Tag(ext.HTTPCode))
			} else {
				assert.Nil(t, finished.Tag(ext.HTTPCode))
			}
			for key, value := range test.wantTags {
				if check, ok := value.(assert.ValueAssertionFunc); ok {
					check(t, finished.Tag(key), key)
					continue
				}
				assert.Equal(t, value, finished.Tag(key), key)
			}
		})
	}

	t.Run("nil span", func(t *testing.T) {
		assert.NotPanics(t, func() {
			newTestConfig().FinishSpan(nil, errors.New("boom"), SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		})
	})

	// A SpanFinish without a protocol finishes the span as one over the unknown protocol.
	t.Run("nil protocol", func(t *testing.T) {
		for _, err := range []error{nil, boom} {
			t.Run(fmt.Sprint(err), func(t *testing.T) {
				mt := startMockTracer(t)
				cfg := newTestConfig(func(c *Config) { c.EnableErrorDetailTags() })
				require.NotPanics(t, func() {
					cfg.FinishSpan(tracer.StartSpan("finish"), err, SpanFinish{Procedure: unaryProcedure})
				})
				finished := requireFinished(t, mt)
				for _, key := range []string{tagConnectErrorCode, tagGRPCStatusCode} {
					assert.Nil(t, finished.Tag(key), key)
				}
				if err == nil {
					assert.Nil(t, finished.Tag(ext.ErrorMsg))
				} else {
					assert.Equal(t, err.Error(), finished.Tag(ext.ErrorMsg))
				}
			})
		}
	})

	// A panic in the error check finishes the span with the panic, which is then re-raised: a caller
	// that runs FinishSpan outside of a Guard leaves no span open.
	t.Run("error check panics", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			value any
		}{{"value", "errCheck panic"}, {"nil", nil}} {
			t.Run(test.name, func(t *testing.T) {
				if test.value == nil && !runsWithPanicNil(t) {
					return
				}
				mt := startMockTracer(t)
				cfg := newTestConfig(errorCheck(func(string, error) bool { panicWith(test.value); return true }))
				o := outcomeOf(t, func() {
					cfg.FinishSpan(tracer.StartSpan("finish"), boom, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
				})
				assert.Equal(t, callOutcome{panicked: true, value: test.value}, o)
				assert.Equal(t, fmt.Sprintf("panic: %v", test.value), requireFinished(t, mt).Tag(ext.ErrorMsg))
			})
		}
	})

	t.Run("the error is checked once", func(t *testing.T) {
		mt := startMockTracer(t)
		counter := new(probeCounter)
		newTestConfig().FinishSpan(tracer.StartSpan("finish"), errors.Join(io.EOF, counter), SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol, Mode: message})
		assert.Nil(t, requireFinished(t, mt).Tag(ext.ErrorMsg))
		assert.Equal(t, 1, counter.probes)
	})

	// With GODEBUG=panicnil=1, recover reports panic(nil) as no panic at all.
	t.Run("error whose Error method panics with nil", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		mt := startMockTracer(t)
		require.NotPanics(t, func() {
			newTestConfig().FinishSpan(tracer.StartSpan("finish"), &formatPanicError{}, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		})
		assert.Contains(t, requireFinished(t, mt).Tag(ext.ErrorMsg), "panics when formatted")
	})
}

// TestErrorCheckArguments checks what the error check is called with: the procedure and the
// error, but only for errors that the non-error code rules did not already suppress.
func TestErrorCheckArguments(t *testing.T) {
	type call struct {
		procedure string
		err       error
	}
	boom := newTestError(CodeInternal, "boom")
	remoteCanceled := newTestError(CodeCanceled, "canceled").withRemote()
	formatPanics := &formatPanicError{value: "Error called"}
	for _, test := range []struct {
		name  string
		err   error
		mode  FinishMode
		panic bool
		want  []call
	}{
		// The span records a substitute, but the error check gets the original error.
		{name: "error whose Error method panics", err: formatPanics, want: []call{{procedure: unaryProcedure, err: formatPanics}}},
		{name: "error", err: boom, want: []call{{procedure: unaryProcedure, err: boom}}},
		{name: "suppressed by code", err: newTestError(CodeCanceled, "canceled")},
		{name: "uncoded cancellation", err: context.Canceled},
		{name: "nil", err: nil},
		{name: "end of stream on a message span", err: io.EOF, mode: FinishMode{AllowEOF: true}},
		{name: "not modified", err: notModifiedError(), mode: FinishMode{allowNotModified: true}},
		{name: "panic", err: boom, panic: true},
		{name: "remote error suppressed by code", err: remoteCanceled},
		// A server reports a remote error as CodeInternal, but the error check gets the original error.
		{name: "remote error on a server call span", err: remoteCanceled, mode: FinishMode{Wire: true}, want: []call{{procedure: unaryProcedure, err: remoteCanceled}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			startMockTracer(t)
			var calls []call
			cfg := newTestConfig(errorCheck(func(procedure string, err error) bool {
				calls = append(calls, call{procedure: procedure, err: err})
				return true
			}))
			require.NotPanics(t, func() {
				cfg.finishSpan(tracer.StartSpan("check"), test.err, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol, Mode: test.mode}, test.panic)
			})
			assert.Equal(t, test.want, calls)
		})
	}

	t.Run("unsafe error", func(t *testing.T) {
		startMockTracer(t)
		var calls []call
		cfg := newTestConfig(errorCheck(func(procedure string, err error) bool {
			calls = append(calls, call{procedure: procedure, err: err})
			return true
		}))
		cfg.FinishSpan(tracer.StartSpan("check"), wrappedDerefError, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		require.Len(t, calls, 1)
		assert.Equal(t, unaryProcedure, calls[0].procedure)
		assert.ErrorContains(t, calls[0].err, "unsafe to inspect")
	})
}

func TestSetErrorDetailTags(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		protocol *Protocol
		want     map[string]any
	}{
		{name: "connect", err: errorWithDetails(CodeInternal), protocol: &connectProtocol, want: map[string]any{"connect.status_details._0": "seconds:1"}},
		{name: "gRPC", err: errorWithDetails(CodeInternal), protocol: &grpcProtocol, want: map[string]any{"grpc.status_details._0": "seconds:1"}},
		{name: "wrapped", err: fmt.Errorf("call: %w", errorWithDetails(CodeInternal)), protocol: &connectProtocol, want: map[string]any{"connect.status_details._0": "seconds:1"}},
		{name: "remote", err: errorWithDetails(CodeInternal).withRemote(), protocol: &connectProtocol, want: map[string]any{"connect.status_details._0": "seconds:1"}},
		{
			name: "skipped details keep their index", err: newTestError(CodeInternal, "boom").withDetails(&testDetail{}, &testDetail{message: &durationpb.Duration{Seconds: 1}}),
			protocol: &connectProtocol, want: map[string]any{"connect.status_details._1": "seconds:1"},
		},
		{
			// connect v1 keeps a nil detail, which its Value method dereferences.
			name: "nil detail is skipped", err: newTestError(CodeInternal, "boom").withDetails(nil, &testDetail{message: &durationpb.Duration{Seconds: 1}}),
			protocol: &connectProtocol, want: map[string]any{"connect.status_details._1": "seconds:1"},
		},
		{name: "not a connect error", err: errors.New("boom"), protocol: &connectProtocol, want: map[string]any{}},
		{name: "typed nil connect error", err: (*testError)(nil), protocol: &connectProtocol, want: map[string]any{}},
		{name: "nil connect error in the chain", err: nilTestErrorWrapper{}, protocol: &connectProtocol, want: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			span := tracer.StartSpan("details")
			require.NotPanics(t, func() { newTestConfig().setErrorDetailTags(span, test.err, test.protocol) })
			span.Finish()
			got := tagsWithPrefix(requireFinished(t, mt), "connect.status_details.")
			maps.Copy(got, tagsWithPrefix(mt.FinishedSpans()[0], "grpc.status_details."))
			assert.Equal(t, test.want, got)
		})
	}
}

// TestSetErrorDetailTagsAllocs checks that looking for the details of an error that has none
// allocates nothing.
func TestSetErrorDetailTagsAllocs(t *testing.T) {
	startMockTracer(t)
	cfg := newTestConfig((*Config).EnableErrorDetailTags)
	span := tracer.StartSpan("details")
	defer span.Finish()
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "not a connect error", err: errors.New("boom")},
		{name: "connect error without details", err: newTestError(CodeInternal, "boom")},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Zero(t, testing.AllocsPerRun(100, func() { cfg.setErrorDetailTags(span, test.err, &connectProtocol) }))
		})
	}
}

func TestUnaryFinishMode(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol *Protocol
		method   string
		wire     bool
		want     FinishMode
	}{
		{name: "Connect GET", protocol: &connectProtocol, method: http.MethodGet, want: FinishMode{allowNotModified: true}},
		{name: "Connect POST", protocol: &connectProtocol, method: http.MethodPost},
		{name: "gRPC", protocol: &grpcProtocol, method: http.MethodGet},
		{name: "unknown protocol", protocol: &unknownProtocol, method: http.MethodGet},
		{name: "in process", protocol: &inProcessProtocol, method: http.MethodGet},
		{name: "server Connect GET", protocol: &connectProtocol, method: http.MethodGet, wire: true, want: FinishMode{allowNotModified: true, Wire: true, HTTP: true}},
		{name: "server gRPC", protocol: &grpcProtocol, method: http.MethodPost, wire: true, want: FinishMode{Wire: true, HTTP: true}},
		{name: "server without an HTTP method", protocol: &connectProtocol, wire: true, want: FinishMode{Wire: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, UnaryFinishMode(test.protocol, test.method, test.wire))
		})
	}
}

func TestIsSuppressedTerminalError(t *testing.T) {
	boom := newTestError(CodeInternal, "boom")
	remoteCanceled := newTestError(CodeCanceled, "canceled").withRemote()
	for _, test := range []struct {
		name    string
		err     error
		mode    FinishMode
		setters []func(*Config)
		want    bool
	}{
		{name: "nil", want: true},
		{name: "error", err: boom},
		{name: "suppressed by code", err: newTestError(CodeCanceled, "canceled"), want: true},
		{name: "NonErrorCodes", err: newTestError(codeNotFound, "nope"), setters: []func(*Config){nonErrorCodes(codeNotFound)}, want: true},
		{name: "uncoded cancellation", err: context.Canceled, setters: []func(*Config){nonErrorCodes()}, want: true},
		{name: "not modified", err: notModifiedError(), mode: FinishMode{allowNotModified: true}, want: true},
		{name: "not modified without a 304", err: notModifiedError()},
		{name: "remote error suppressed by code", err: remoteCanceled, want: true},
		{name: "remote error on a server", err: remoteCanceled, mode: FinishMode{Wire: true}},
		{
			name: "the error check is not called", err: boom,
			setters: []func(*Config){errorCheck(func(string, error) bool { panic("error check called") })},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, newTestConfig(test.setters...).isSuppressedTerminalError(test.err, test.mode))
		})
	}
}

// afterCall is a call to the after function of FinishOnReturn.
type afterCall struct {
	err     error
	isPanic bool
}

// traceOp runs op as a function whose deferred FinishOnReturn finishes a span started after the
// defer, with *f. It returns the calls to after, and how the function ended.
func traceOp(t *testing.T, cfg *Config, f *SpanFinish, withAfter bool, op func() error) (calls []afterCall, o callOutcome) {
	t.Helper()
	var after func(err error, isPanic bool)
	if withAfter {
		after = func(err error, isPanic bool) { calls = append(calls, afterCall{err: err, isPanic: isPanic}) }
	}
	o = outcomeOf(t, func() {
		_ = func() (err error) {
			var span *tracer.Span
			var guard Guard
			defer cfg.FinishOnReturn(&guard, &span, &err, f, after)
			guard.Run(func() {
				span = tracer.StartSpan("op")
				err = op()
			})
			return err
		}()
	})
	return calls, o
}

// callOutcome is how a call ended: by returning, with a panic, possibly with a nil value, or with
// runtime.Goexit.
type callOutcome struct {
	panicked bool
	value    any
	goexit   bool
}

// outcomeOf runs fn on its own goroutine and reports how it ended. It fails the test if fn does not
// end in time, which would mean a deadlock.
func outcomeOf(t *testing.T, fn func()) callOutcome {
	t.Helper()
	done := make(chan callOutcome, 1)
	go func() {
		var o callOutcome
		ended := false
		defer func() {
			if !ended {
				o = callOutcome{goexit: true}
			}
			done <- o
		}()
		func() {
			returned := false
			defer func() {
				if !returned {
					o.panicked, o.value = true, recover()
				}
			}()
			fn()
			returned = true
		}()
		// Only a return or a recovered panic gets here: runtime.Goexit does not.
		ended = true
	}()
	select {
	case o := <-done:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end")
		return callOutcome{}
	}
}

// panicWith panics with value, which may be nil.
func panicWith(value any) { panic(value) }

// recoversNilPanics reports whether recover returns nil for panic(nil), as it does with
// GODEBUG=panicnil=1.
func recoversNilPanics() (recoversNil bool) {
	defer func() { recoversNil = recover() == nil }()
	panicWith(nil)
	return false
}

// runsWithPanicNil reports whether the test runs with GODEBUG=panicnil=1. Otherwise it runs the
// test again in a child process with that setting, which is process-wide, and fails unless it
// passes there; the caller then returns.
func runsWithPanicNil(t *testing.T) bool {
	t.Helper()
	if recoversNilPanics() {
		return true
	}
	var pattern []string
	for name := range strings.SplitSeq(t.Name(), "/") {
		pattern = append(pattern, "^"+regexp.QuoteMeta(name)+"$")
	}
	args := []string{"-test.run=" + strings.Join(pattern, "/"), "-test.v", "-test.count=1"}
	for _, arg := range os.Args[1:] {
		// With go test -cover, the child's coverage joins the parent's.
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			args = append(args, arg)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GODEBUG="+strings.TrimPrefix(os.Getenv("GODEBUG")+",panicnil=1", ","))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "child process output:\n%s", out)
	assert.Contains(t, string(out), "--- PASS: "+t.Name()+" ", "child process output:\n%s", out)
	return false
}

// panicSite panics with a value that tests look for in stack traces.
//
//go:noinline
func panicSite() error { panic("panic site") }

func TestFinishOnReturn(t *testing.T) {
	boom := newTestError(CodeInternal, "boom")
	notFound := newTestError(codeNotFound, "not found")
	panicking := errorCheck(func(string, error) bool { panic("error check") })
	formatPanics := &formatPanicError{value: "Error called"}
	for _, test := range []struct {
		name    string
		setters []func(*Config)
		// op runs with the span's finish, which it may change.
		op func(f *SpanFinish) error
		// wantError is a substring of the error that the span records and, unless wantAfter is set,
		// of the one that after gets; empty if it gets nil.
		wantError string
		// wantAfter, if set, is the error that after gets.
		wantAfter     error
		wantPanic     any
		wantSpanError bool // the span records wantError
		want304       bool
		wantCode      string // the span's status, if set
	}{
		{name: "nil error", op: func(*SpanFinish) error { return nil }},
		{name: "error", op: func(*SpanFinish) error { return boom }, wantError: "boom", wantSpanError: true},
		{
			name: "the finish may change after the defer",
			op: func(f *SpanFinish) error {
				f.Mode = UnaryFinishMode(&connectProtocol, http.MethodGet, false)
				return notModifiedError()
			},
			wantError: "not modified", want304: true,
		},
		{name: "unsafe error", op: func(*SpanFinish) error { return wrappedDerefError }, wantError: "unsafe to inspect", wantSpanError: true},
		{name: "panic", op: func(*SpanFinish) error { panic(boom) }, wantError: "boom", wantPanic: boom, wantSpanError: true},
		{name: "panic with a value", op: func(*SpanFinish) error { panic("boom") }, wantError: "panic: boom", wantPanic: "boom", wantSpanError: true},
		{
			name: "panic in the error check", setters: []func(*Config){panicking}, op: func(*SpanFinish) error { return boom },
			wantError: "panic: error check", wantPanic: "error check", wantSpanError: true,
		},
		// The server transports send a handler's panic, also one that the interceptor re-raises from
		// the error check, as CodeInternal.
		{
			name: "panic on a server",
			op: func(f *SpanFinish) error {
				f.Mode = FinishMode{Wire: true}
				panic(notFound)
			},
			wantError: "not found", wantPanic: notFound, wantSpanError: true, wantCode: "internal",
		},
		{
			name: "panic in the error check on a server", setters: []func(*Config){panicking},
			op: func(f *SpanFinish) error {
				f.Mode = FinishMode{Wire: true}
				return notFound
			},
			wantError: "panic: error check", wantPanic: "error check", wantSpanError: true, wantCode: "internal",
		},
		{
			name: "error whose Error method panics", op: func(*SpanFinish) error { return formatPanics },
			wantError: "panics when formatted", wantAfter: formatPanics, wantSpanError: true,
		},
		{
			name: "panic with an error whose Error method panics", op: func(*SpanFinish) error { panic(formatPanics) },
			wantError: "panics when formatted", wantAfter: formatPanics, wantPanic: formatPanics, wantSpanError: true,
		},
		{
			name:    "panic in the error check with an error whose Error method panics",
			setters: []func(*Config){errorCheck(func(string, error) bool { panic(formatPanics) })}, op: func(*SpanFinish) error { return boom },
			wantError: "panics when formatted", wantAfter: formatPanics, wantPanic: formatPanics, wantSpanError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, withAfter := range []bool{true, false} {
				mt := startMockTracer(t)
				f := SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol}
				calls, o := traceOp(t, newTestConfig(test.setters...), &f, withAfter, func() error { return test.op(&f) })
				assert.Equal(t, callOutcome{panicked: test.wantPanic != nil, value: test.wantPanic}, o)
				finished := requireFinished(t, mt)
				if test.wantSpanError {
					assert.Contains(t, finished.Tag(ext.ErrorMsg), test.wantError)
				} else {
					assert.Nil(t, finished.Tag(ext.ErrorMsg))
				}
				if test.want304 {
					assert.Equal(t, "304", finished.Tag(ext.HTTPCode))
				}
				if test.wantCode != "" {
					assert.Equal(t, test.wantCode, finished.Tag(connectProtocol.statusCodeTag))
				}
				if !withAfter {
					assert.Empty(t, calls)
					continue
				}
				require.Len(t, calls, 1)
				assert.Equal(t, test.wantPanic != nil, calls[0].isPanic)
				switch {
				case test.wantAfter != nil:
					assert.True(t, calls[0].err == test.wantAfter, "after got a %T, want the original error", calls[0].err)
				case test.wantError == "":
					assert.NoError(t, calls[0].err)
				default:
					assert.ErrorContains(t, calls[0].err, test.wantError)
				}
			}
		})
	}

	// The span is finished while the panic is in flight.
	t.Run("the recorded stack shows where the panic happened", func(t *testing.T) {
		mt := startMockTracer(t)
		_, o := traceOp(t, newTestConfig(), &SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol}, false, panicSite)
		assert.Equal(t, callOutcome{panicked: true, value: "panic site"}, o)
		assert.Contains(t, requireFinished(t, mt).Tag(ext.ErrorHandlingStack), "connectshared.panicSite")
	})

	// recover reports nil both for runtime.Goexit and, with GODEBUG=panicnil=1, for panic(nil). A
	// Goexit stays one, and finishes the span as a return does.
	goexit := func(t *testing.T) {
		mt := startMockTracer(t)
		calls, o := traceOp(t, newTestConfig(), &SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol}, true, func() error {
			runtime.Goexit()
			return boom
		})
		assert.Equal(t, callOutcome{goexit: true}, o)
		assert.Nil(t, requireFinished(t, mt).Tag(ext.ErrorMsg))
		assert.Equal(t, []afterCall{{}}, calls)
	}
	t.Run("runtime.Goexit", goexit)
	t.Run("runtime.Goexit with panicnil", func(t *testing.T) {
		if runsWithPanicNil(t) {
			goexit(t)
		}
	})

	t.Run("panic(nil)", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		for _, test := range []struct {
			name    string
			setters []func(*Config)
			op      func() error
		}{
			{name: "in the traced function", op: func() error { panicWith(nil); return nil }},
			{
				name: "in the error check", op: func() error { return boom },
				setters: []func(*Config){errorCheck(func(string, error) bool { panicWith(nil); return true })},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				calls, o := traceOp(t, newTestConfig(test.setters...), &SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol}, true, test.op)
				assert.Equal(t, callOutcome{panicked: true}, o)
				assert.Equal(t, "panic: <nil>", requireFinished(t, mt).Tag(ext.ErrorMsg))
				require.Len(t, calls, 1)
				assert.True(t, calls[0].isPanic)
				assert.EqualError(t, calls[0].err, "panic: <nil>")
			})
		}
	})

	t.Run("nil span", func(t *testing.T) {
		mt := startMockTracer(t)
		var calls []afterCall
		_ = func() (err error) {
			var span *tracer.Span
			var guard Guard
			defer newTestConfig().FinishOnReturn(&guard, &span, &err, &SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol}, func(err error, isPanic bool) {
				calls = append(calls, afterCall{err: err, isPanic: isPanic})
			})
			guard.Run(func() { err = boom })
			return err
		}()
		assert.Empty(t, mt.FinishedSpans())
		assert.Equal(t, []afterCall{{err: boom}}, calls)
	})
}

func TestFinishOnOpenPanic(t *testing.T) {
	boom := newTestError(CodeInternal, "boom")
	// open runs a call that registers FinishOnOpenPanic with span and panics with value, unless it is
	// nil. It returns the value that the call panicked with.
	open := func(span *tracer.Span, protocol func() *Protocol, value any) (recovered any) {
		defer func() { recovered = recover() }()
		var guard Guard
		defer newTestConfig().FinishOnOpenPanic(&guard, &span, protocol, unaryProcedure)
		guard.Run(func() {
			if value != nil {
				panic(value)
			}
		})
		return nil
	}

	t.Run("no panic", func(t *testing.T) {
		mt := startMockTracer(t)
		span := tracer.StartSpan("call")
		assert.Nil(t, open(span, func() *Protocol { panic("protocol called") }, nil))
		assert.Empty(t, mt.FinishedSpans())
		span.Finish()
	})

	t.Run("panic", func(t *testing.T) {
		mt := startMockTracer(t)
		var calls int
		protocol := func() *Protocol {
			calls++
			return &grpcProtocol
		}
		assert.Same(t, boom, open(tracer.StartSpan("call"), protocol, boom))
		assert.Equal(t, 1, calls)
		finished := requireFinished(t, mt)
		assert.EqualValues(t, uint32(CodeInternal), finished.Tag(tagGRPCStatusCode))
		assert.Contains(t, finished.Tag(ext.ErrorMsg), "boom")
	})

	t.Run("panic with a suppressed code", func(t *testing.T) {
		mt := startMockTracer(t)
		canceled := newTestError(CodeCanceled, "canceled")
		assert.Same(t, canceled, open(tracer.StartSpan("call"), func() *Protocol { return &connectProtocol }, canceled))
		finished := requireFinished(t, mt)
		assert.Equal(t, "canceled", finished.Tag(tagConnectErrorCode))
		assert.Contains(t, finished.Tag(ext.ErrorMsg), "canceled")
	})

	// A stream that panics before a transport reports its protocol has none of the protocol's tags.
	t.Run("panic before the protocol is known", func(t *testing.T) {
		mt := startMockTracer(t)
		assert.Same(t, boom, open(tracer.StartSpan("call"), func() *Protocol { return ProtocolFor("") }, boom))
		finished := requireFinished(t, mt)
		for _, key := range []string{ext.RPCSystem, ext.GRPCFullMethod, tagConnectErrorCode, tagGRPCStatusCode} {
			assert.Nil(t, finished.Tag(key), key)
		}
		assert.Contains(t, finished.Tag(ext.ErrorMsg), "boom")
	})

	t.Run("nil span", func(t *testing.T) {
		mt := startMockTracer(t)
		assert.Equal(t, "boom", open(nil, func() *Protocol { return &connectProtocol }, "boom"))
		assert.Empty(t, mt.FinishedSpans())
	})

	t.Run("panic with an error whose Error method panics", func(t *testing.T) {
		mt := startMockTracer(t)
		formatPanics := &formatPanicError{value: "Error called"}
		assert.Same(t, formatPanics, open(tracer.StartSpan("call"), func() *Protocol { return &connectProtocol }, formatPanics))
		assert.Contains(t, requireFinished(t, mt).Tag(ext.ErrorMsg), "panics when formatted")
	})

	t.Run("panic(nil)", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		mt := startMockTracer(t)
		span := tracer.StartSpan("call")
		o := outcomeOf(t, func() {
			var guard Guard
			defer newTestConfig().FinishOnOpenPanic(&guard, &span, func() *Protocol { return &connectProtocol }, unaryProcedure)
			guard.Run(func() { panicWith(nil) })
		})
		assert.Equal(t, callOutcome{panicked: true}, o)
		assert.Equal(t, "panic: <nil>", requireFinished(t, mt).Tag(ext.ErrorMsg))
	})
}

func TestFinishTerminal(t *testing.T) {
	boom := newTestError(CodeInternal, "boom")
	other := newTestError(codeAborted, "other")
	rejectsBoom := errorCheck(func(_ string, err error) bool { return !errors.Is(err, boom) })
	formatPanics := &formatPanicError{value: "Error called"}
	for _, test := range []struct {
		name      string
		err       error
		isPanic   bool
		fallbacks []error
		mode      FinishMode
		setters   []func(*Config)
		wantCode  any
		wantError string
		want304   bool
	}{
		{name: "nil error"},
		{name: "error", err: boom, wantCode: "internal", wantError: "boom"},
		{name: "suppressed error", err: newTestError(CodeCanceled, "canceled"), wantCode: "canceled"},
		{name: "panic", err: newTestError(CodeCanceled, "canceled"), isPanic: true, wantCode: "canceled", wantError: "canceled"},
		{name: "not modified", err: notModifiedError(), mode: FinishMode{allowNotModified: true}, want304: true},
		{name: "fallbacks are not classified once an error is recorded", err: boom, fallbacks: []error{other}, wantCode: "internal", wantError: "boom"},
		{
			name: "the first fallback that the error check accepts", err: boom, fallbacks: []error{boom, other, newTestError(CodeInternal, "last")},
			setters: []func(*Config){rejectsBoom}, wantCode: "aborted", wantError: "other",
		},
		{
			name: "the error check rejects every error", err: boom, fallbacks: []error{boom},
			setters: []func(*Config){rejectsBoom}, wantCode: "internal",
		},
		{name: "unsafe error", err: wrappedDerefError, wantCode: "unknown", wantError: "unsafe to inspect"},
		{
			name: "unsafe fallback", err: boom, fallbacks: []error{wrappedDerefError},
			setters: []func(*Config){rejectsBoom}, wantCode: "unknown", wantError: "unsafe to inspect",
		},
		{name: "error whose Error method panics", err: formatPanics, wantCode: "unknown", wantError: "panics when formatted"},
		{name: "panic with an error whose Error method panics", err: formatPanics, isPanic: true, wantCode: "unknown", wantError: "panics when formatted"},
		{
			name: "fallback whose Error method panics", err: boom, fallbacks: []error{&formatPanicError{value: "Error called", cause: other}},
			setters: []func(*Config){rejectsBoom}, wantCode: "aborted", wantError: "panics when formatted",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			f := SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol, Mode: test.mode}
			var panicked bool
			o := outcomeOf(t, func() {
				panicked, _ = newTestConfig(test.setters...).finishTerminal(tracer.StartSpan("call"), test.err, test.isPanic, test.fallbacks, f)
			})
			assert.Equal(t, callOutcome{}, o)
			assert.False(t, panicked)
			finished := requireFinished(t, mt)
			assert.EqualValues(t, test.wantCode, finished.Tag(tagConnectErrorCode))
			if test.wantError == "" {
				assert.Nil(t, finished.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, finished.Tag(ext.ErrorMsg), test.wantError)
			}
			if test.want304 {
				assert.Equal(t, "304", finished.Tag(ext.HTTPCode))
			}
		})
	}

	t.Run("panic in the error check", func(t *testing.T) {
		mt := startMockTracer(t)
		cfg := newTestConfig(errorCheck(func(string, error) bool { panic("error check") }))
		var panicked bool
		var value any
		require.NotPanics(t, func() {
			panicked, value = cfg.finishTerminal(tracer.StartSpan("call"), boom, false, nil, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		})
		assert.True(t, panicked)
		assert.Equal(t, "error check", value)
		finished := requireFinished(t, mt)
		assert.Equal(t, "unknown", finished.Tag(tagConnectErrorCode))
		assert.Contains(t, finished.Tag(ext.ErrorMsg), "panic: error check")
	})

	t.Run("panic in the error check with an error whose Error method panics", func(t *testing.T) {
		mt := startMockTracer(t)
		cfg := newTestConfig(errorCheck(func(string, error) bool { panic(formatPanics) }))
		var panicked bool
		var value any
		o := outcomeOf(t, func() {
			panicked, value = cfg.finishTerminal(tracer.StartSpan("call"), boom, false, nil, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		})
		assert.Equal(t, callOutcome{}, o)
		assert.True(t, panicked)
		assert.Same(t, formatPanics, value)
		assert.Contains(t, requireFinished(t, mt).Tag(ext.ErrorMsg), "panics when formatted")
	})

	t.Run("panic(nil) in the error check", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		mt := startMockTracer(t)
		cfg := newTestConfig(errorCheck(func(string, error) bool { panicWith(nil); return true }))
		var panicked bool
		value := any("unset")
		o := outcomeOf(t, func() {
			panicked, value = cfg.finishTerminal(tracer.StartSpan("call"), boom, false, nil, SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol})
		})
		assert.Equal(t, callOutcome{}, o)
		assert.True(t, panicked)
		assert.Nil(t, value)
		assert.Equal(t, "panic: <nil>", requireFinished(t, mt).Tag(ext.ErrorMsg))
	})
}

// TestClassificationAllocs checks that classifying how a span finishes allocates nothing.
func TestClassificationAllocs(t *testing.T) {
	cfg := newTestConfig()
	for _, test := range []struct {
		name string
		err  error
		mode FinishMode
	}{
		{name: "nil error"},
		{name: "expected EOF", err: io.EOF, mode: FinishMode{AllowEOF: true}},
		{name: "coded error", err: newTestError(CodeInternal, "boom")},
		{name: "remote error on a server", err: newTestError(codeNotFound, "upstream").withRemote(), mode: FinishMode{Wire: true}},
		{name: "uncoded os deadline on an HTTP server", err: fmt.Errorf("query: %w", os.ErrDeadlineExceeded), mode: FinishMode{Wire: true, HTTP: true}},
		{name: "uncoded cancellation", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Zero(t, testing.AllocsPerRun(100, func() {
				err := cfg.lib.normalizeError(test.err)
				_ = cfg.finishOutcomeFor(err, unaryProcedure, test.mode)
				_ = cfg.isSuppressedTerminalError(err, test.mode)
				_ = cfg.IsExpectedStreamEOF(err)
			}))
		})
	}
}
