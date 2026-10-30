// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"cmp"
	"context"
	"errors"
	"io"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// callInfoWithParent returns a server CallInfo with protocol whose request header propagates
// parent.
func callInfoWithParent(t *testing.T, protocol string, parent *tracer.Span) *connectrpc.CallInfo {
	t.Helper()
	info := newServerCallInfo(protocol)
	require.NoError(t, tracer.Inject(parent.Context(), headerCarrier{header: info.RequestHeader()}))
	return info
}

// serverTracedClient returns an untraced client of a rig built with opts, whose server is traced
// with traceOpts.
func serverTracedClient(t *testing.T, tt testTransport, opts rigOptions, traceOpts ...Option) *connectrpc.Client {
	t.Helper()
	opts.serverInterceptors = []connectrpc.ServerInterceptor{NewServerInterceptor(traceOpts...)}
	return newTestRig(t, tt, opts).client()
}

// requireServerCallSpan waits for the one server call span of a server-only traced unary RPC.
func requireServerCallSpan(t *testing.T, mt mocktracer.Tracer) *mocktracer.Span {
	t.Helper()
	calls := spansNamed(waitForSpans(t, mt, 1), operationServer)
	require.Len(t, calls, 1)
	return calls[0]
}

// panickingHandler panics. Tests look for it in stack traces.
//
//go:noinline
func panickingHandler(context.Context, connectrpc.Spec, connectrpc.ServerStream) error {
	panic("handler panic")
}

func TestUnaryHandler(t *testing.T) {
	var typedNilConnectErr *connectrpc.Error
	errorPanics := &derefError{}
	for _, test := range []struct {
		name      string
		handler   connectrpc.ServerFunc
		wantPanic any
		// panicNil runs the case with GODEBUG=panicnil=1, with which recover reports a panic(nil) as
		// no panic.
		panicNil  bool
		goexit    bool   // the handler calls runtime.Goexit
		wantError string // a substring of the recorded error; empty if none may be recorded
		wantStack string // a substring of the recorded error's stack
	}{
		{name: "success", handler: handleUnary},
		{name: "panic", handler: panickingHandler, wantPanic: "handler panic", wantError: "handler panic", wantStack: "panickingHandler"},
		{
			name: "typed nil error", wantError: "unsafe to inspect",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { return typedNilConnectErr },
		},
		{
			name: "error that panics when unwrapped", wantError: "unsafe to inspect",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { return wrappedDerefError },
		},
		{
			name: "panic with an error that panics when unwrapped", wantPanic: wrappedDerefError, wantError: "unsafe to inspect",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panic(wrappedDerefError) },
		},
		// The tracer calls Error while it holds the span's lock.
		{
			name: "error whose Error method panics", wantError: "panics when formatted",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { return &derefError{} },
		},
		{
			name: "panic with an error whose Error method panics", wantPanic: errorPanics, wantError: "panics when formatted",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panic(errorPanics) },
		},
		{
			name: "panic(nil)", panicNil: true, wantError: "panic: <nil>",
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panicWith(nil); return nil },
		},
		{
			// recover reports nil for runtime.Goexit too, which finishes the span as a return does.
			name: "runtime.Goexit", goexit: true,
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { runtime.Goexit(); return nil },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
			mt := startMockTracer(t)
			parent := tracer.StartSpan("parent")
			info := callInfoWithParent(t, connectrpc.ProtocolNameConnect, parent)
			o := outcomeOf(t, func() {
				_ = serveFake(context.Background(), unarySpec, info, &fakeServerStream{}, test.handler, NewServerInterceptor())
			})
			assert.Equal(t, callOutcome{panicked: test.wantPanic != nil || test.panicNil, value: test.wantPanic, goexit: test.goexit}, o)
			parent.Finish()

			spans := spansNamed(mt.FinishedSpans(), operationServer)
			require.Len(t, spans, 1)
			span := spans[0]
			assert.Equal(t, parent.Context().SpanID(), span.ParentID())
			assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
			assert.Equal(t, ext.RPCSystemConnectRPC, span.Tag(ext.RPCSystem))
			assert.Equal(t, methodKindUnary, span.Tag(tagMethodKind))
			// The CallInfo's peer is the client: a server span has no destination.
			assert.Nil(t, span.Tag(ext.NetworkDestinationIP))
			if test.wantError == "" {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, span.Tag(ext.ErrorMsg), test.wantError)
			}
			if test.wantStack != "" {
				assert.Contains(t, span.Tag(ext.ErrorHandlingStack), test.wantStack)
			}
		})
	}

	t.Run("request tags", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			opts     []Option
			protocol string
			receive  func() error
			// wantWrapped is whether the handler gets a wrapper rather than the transport's stream.
			wantWrapped bool
			wantTag     string // the tag with the request, if any
			wantError   string
		}{
			{name: "disabled", protocol: connectrpc.ProtocolNameConnect},
			{name: "connect", opts: []Option{WithRequestTags()}, protocol: connectrpc.ProtocolNameConnect, wantWrapped: true, wantTag: tagConnectRequest},
			{name: "grpc", opts: []Option{WithRequestTags()}, protocol: connectrpc.ProtocolNameGRPC, wantWrapped: true, wantTag: tagGRPCRequest},
			{
				name: "failed Receive", opts: []Option{WithRequestTags()}, protocol: connectrpc.ProtocolNameConnect,
				receive: returns(connectrpc.NewError(connectrpc.CodeInvalidArgument, "bad request")), wantWrapped: true, wantError: "bad request",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				stream := &fakeServerStream{receive: test.receive}
				var got connectrpc.ServerStream
				handler := func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
					got = stream
					if err := stream.Receive(wrapperspb.String("hello")); err != nil {
						return err
					}
					return stream.Send(wrapperspb.String("hello"))
				}
				err := serveFake(context.Background(), unarySpec, newServerCallInfo(test.protocol), stream, handler, NewServerInterceptor(test.opts...))
				if test.wantError != "" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				if test.wantWrapped {
					assert.NotSame(t, stream, got)
				} else {
					assert.Same(t, stream, got)
				}

				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				for _, tag := range []string{tagConnectRequest, tagGRPCRequest} {
					if tag == test.wantTag {
						assert.Equal(t, `"hello"`, spans[0].Tag(tag))
					} else {
						assert.Nil(t, spans[0].Tag(tag), tag)
					}
				}
				if test.wantError != "" {
					assert.Contains(t, spans[0].Tag(ext.ErrorMsg), test.wantError)
				} else {
					assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
				}
			})
		}
	})

	// The call span reports the request headers that the server received, not the handler's
	// changes.
	t.Run("metadata tags", func(t *testing.T) {
		mt := startMockTracer(t)
		info := newServerCallInfo(connectrpc.ProtocolNameConnect)
		info.RequestHeader().Set("X-Meta", "value")
		err := serveFake(context.Background(), unarySpec, info, &fakeServerStream{}, func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
			info.RequestHeader().Set("X-Meta", "changed")
			info.RequestHeader().Set("X-Late", "late")
			return handleUnary(ctx, spec, stream)
		}, NewServerInterceptor(WithMetadataTags()))
		require.NoError(t, err)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "value", metadataTag(spans[0], "x-meta"))
		assert.Nil(t, metadataTag(spans[0], "x-late"))
	})

	// Only a Connect GET is answered with a 304: a not-modified error is a real error otherwise.
	t.Run("not modified", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			transport testTransport
			get       bool
			want304   bool
		}{
			{name: "connect GET", transport: connectTransport, get: true, want304: true},
			{name: "connect POST", transport: connectTransport},
			{name: "grpc", transport: grpcTransport, get: true},
			{name: "grpcweb", transport: grpcWebTransport, get: true},
			{name: "inprocess", transport: inProcessTransport},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var opts rigOptions
				if test.get {
					opts.transportOptions = []connecthttp.Option{connecthttp.WithHTTPGet()}
				}
				_, err := callUnary(context.Background(), serverTracedClient(t, test.transport, opts), valueNotModified)
				require.Error(t, err)

				span := requireServerCallSpan(t, mt)
				if test.want304 {
					assert.True(t, connecthttp.IsNotModifiedError(err), "%v", err)
					assert.Equal(t, "304", span.Tag(ext.HTTPCode))
					assert.Nil(t, span.Tag(ext.ErrorMsg))
					assert.Nil(t, span.Tag(tagConnectErrorCode))
					return
				}
				assert.Nil(t, span.Tag(ext.HTTPCode))
				assert.Contains(t, span.Tag(ext.ErrorMsg), "not modified")
				if test.transport.grpc {
					assert.EqualValues(t, connectrpc.CodeUnknown, span.Tag(tagGRPCStatusCode))
				} else {
					assert.Equal(t, "unknown", span.Tag(tagConnectErrorCode))
				}
			})
		}
	})

	// A remote error that a handler returns, such as the error of a call it made, is sent as
	// CodeInternal: the span reports that code, but errCheck gets the handler's error.
	t.Run("remote error", func(t *testing.T) {
		for _, transport := range testTransports {
			t.Run(transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var (
					mu      sync.Mutex
					checked []error
				)
				errCheck := func(_ string, err error) bool {
					mu.Lock()
					defer mu.Unlock()
					checked = append(checked, err)
					return true
				}
				_, err := callUnary(context.Background(), serverTracedClient(t, transport, rigOptions{}, WithErrorCheck(errCheck)), valueRemoteError)
				require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)

				span := requireServerCallSpan(t, mt)
				if transport.grpc {
					assert.EqualValues(t, connectrpc.CodeInternal, span.Tag(tagGRPCStatusCode))
				} else {
					assert.Equal(t, "internal", span.Tag(tagConnectErrorCode))
				}
				assert.Contains(t, span.Tag(ext.ErrorMsg), "upstream not found")
				mu.Lock()
				defer mu.Unlock()
				require.Len(t, checked, 1)
				original, ok := errors.AsType[*connectrpc.Error](checked[0])
				require.True(t, ok, "%#v", checked[0])
				assert.Equal(t, connectrpc.CodeNotFound, original.Code())
				assert.True(t, original.IsRemote())
			})
		}

		remote := testHandlerError(valueRemoteError)
		for _, spec := range []connectrpc.Spec{unarySpec, bidiSpec} {
			for _, test := range []struct {
				name         string
				opts         []Option
				wantRecorded bool
			}{
				{name: "default", wantRecorded: true},
				{name: "NonErrorCodes with the sent code", opts: []Option{NonErrorCodes(connectrpc.CodeInternal)}},
				{name: "NonErrorCodes with the handler's code", opts: []Option{NonErrorCodes(connectrpc.CodeNotFound)}, wantRecorded: true},
			} {
				t.Run(methodKind(spec.StreamType)+"/"+test.name, func(t *testing.T) {
					mt := startMockTracer(t)
					err := traceFakeServer(context.Background(), spec, &fakeServerStream{}, func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error {
						return remote
					}, test.opts...)
					require.Same(t, remote, err)
					spans := mt.FinishedSpans()
					require.Len(t, spans, 1)
					assert.Equal(t, "internal", spans[0].Tag(tagConnectErrorCode))
					if test.wantRecorded {
						assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "upstream not found")
					} else {
						assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
					}
				})
			}
		}
	})
}

// TestServerWithoutCallInfo checks a server interceptor chain called without connect.Server.Call,
// and so without a CallInfo: every interceptor of the chain sees the same (nil) CallInfo, so the
// call is traced once. No transport reported its protocol, so it has none of the protocol's tags.
func TestServerWithoutCallInfo(t *testing.T) {
	for _, test := range []struct {
		name         string
		spec         connectrpc.Spec
		opts         []Option
		wantCalls    int
		wantMessages int
	}{
		{name: "unary", spec: unarySpec, wantCalls: 1},
		{name: "bidi", spec: bidiSpec, wantCalls: 1, wantMessages: 2},
		{name: "bidi without a call span", spec: bidiSpec, opts: []Option{WithStreamCalls(false)}, wantMessages: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			parent := tracer.StartSpan("parent")
			var hasCallInfo bool
			handler := func(ctx context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
				_, hasCallInfo = connectrpc.CallInfoForServerContext(ctx)
				if err := stream.Receive(&wrapperspb.StringValue{}); err != nil {
					return err
				}
				return stream.Send(wrapperspb.String("hello"))
			}
			outer := NewServerInterceptor(slices.Concat(test.opts, []Option{WithService("outer"), WithMetadataTags()})...)
			inner := NewServerInterceptor(slices.Concat(test.opts, []Option{WithService("inner")})...)
			err := outer(inner(handler))(tracer.ContextWithSpan(context.Background(), parent), test.spec, &fakeServerStream{})
			require.NoError(t, err)
			assert.False(t, hasCallInfo)
			parent.Finish()

			spans := mt.FinishedSpans()
			calls := spansNamed(spans, operationServer)
			require.Len(t, calls, test.wantCalls)
			messages := spansNamed(spans, operationMessage)
			require.Len(t, messages, test.wantMessages)
			messageParent := parent.Context().SpanID()
			if len(calls) == 1 {
				assert.Equal(t, parent.Context().SpanID(), calls[0].ParentID())
				messageParent = calls[0].SpanID()
			}
			for _, span := range messages {
				assert.Equal(t, messageParent, span.ParentID())
			}
			for _, span := range slices.Concat(calls, messages) {
				assert.Equal(t, "outer", span.Tag(ext.ServiceName), span.OperationName())
				assert.Nil(t, span.Tag(ext.RPCSystem), span.OperationName())
				assert.Empty(t, tagsWithPrefix(span, tagRequestMetadataPrefix), span.OperationName())
				assert.Nil(t, span.Tag(ext.ErrorMsg), span.OperationName())
			}
		})
	}
}

// TestStreamingHandlerPanics checks that a panic finishes every span with an error and is
// re-raised unchanged. The call span reports CodeInternal, which the transports send for a panic,
// and message spans the panic's own code.
func TestStreamingHandlerPanics(t *testing.T) {
	errorPanics := &derefError{}
	codedPanic := connectrpc.NewError(connectrpc.CodeNotFound, "coded panic")
	for _, test := range []struct {
		name      string
		opts      []Option
		stream    *fakeServerStream
		handler   connectrpc.ServerFunc
		wantPanic any
		// panicNil runs the case with GODEBUG=panicnil=1, with which recover reports a panic(nil),
		// such as the one wantPanic then stands for, as no panic.
		panicNil  bool
		wantSpans int
		// wantMessageCode is the status of the message spans, if not CodeUnknown.
		wantMessageCode connectrpc.Code
	}{
		{
			name: "handler", stream: &fakeServerStream{}, wantPanic: "handler panic", wantSpans: 1,
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panic("handler panic") },
		},
		{name: "Receive", stream: &fakeServerStream{receive: panics("receive panic")}, handler: receiveMessage, wantPanic: "receive panic", wantSpans: 2},
		{name: "Send", stream: &fakeServerStream{send: panics("send panic")}, handler: sendMessage, wantPanic: "send panic", wantSpans: 2},
		{
			name: "Receive with a coded error", stream: &fakeServerStream{receive: panics(codedPanic)}, handler: receiveMessage,
			wantPanic: codedPanic, wantSpans: 2, wantMessageCode: connectrpc.CodeNotFound,
		},
		{
			name: "Receive with an error that panics when unwrapped", stream: &fakeServerStream{receive: panics(wrappedDerefError)},
			handler: receiveMessage, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Send with an error that panics when unwrapped", stream: &fakeServerStream{send: panics(wrappedDerefError)},
			handler: sendMessage, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "SendHeaders", stream: &fakeServerStream{sendHeaders: panics("send headers panic")}, wantPanic: "send headers panic", wantSpans: 1,
			handler: func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
				return stream.SendHeaders()
			},
		},
		{
			name: "Receive without a call span", opts: []Option{WithStreamCalls(false)}, stream: &fakeServerStream{receive: panics("receive panic")},
			handler: receiveMessage, wantPanic: "receive panic", wantSpans: 1,
		},
		{
			name: "handler with an error whose Error method panics", stream: &fakeServerStream{}, wantPanic: errorPanics, wantSpans: 1,
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panic(errorPanics) },
		},
		{
			name: "Receive with an error whose Error method panics", stream: &fakeServerStream{receive: panics(errorPanics)},
			handler: receiveMessage, wantPanic: errorPanics, wantSpans: 2,
		},
		{
			name: "handler with nil", stream: &fakeServerStream{}, panicNil: true, wantSpans: 1,
			handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { panicWith(nil); return nil },
		},
		{name: "Send with nil", stream: &fakeServerStream{send: panics(nil)}, handler: sendMessage, panicNil: true, wantSpans: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
			mt := startMockTracer(t)
			assert.Equal(t, callOutcome{panicked: true, value: test.wantPanic}, outcomeOf(t, func() {
				_ = traceFakeServer(context.Background(), bidiSpec, test.stream, test.handler, test.opts...)
			}))
			spans := mt.FinishedSpans()
			require.Len(t, spans, test.wantSpans)
			for _, span := range spans {
				assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
				want := cmp.Or(test.wantMessageCode, connectrpc.CodeUnknown)
				if span.OperationName() == operationServer {
					want = connectrpc.CodeInternal
				}
				assertStatus(t, connectTransport, span, want)
			}
		})
	}
}

// TestStreamingHandlerUnsafeErrors checks errors whose methods panic because of a nil pointer.
func TestStreamingHandlerUnsafeErrors(t *testing.T) {
	returnsErr := func(err error) connectrpc.ServerFunc {
		return func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { return err }
	}
	ops := []struct {
		name         string
		set          func(*fakeServerStream, error)
		handler      func(error) connectrpc.ServerFunc
		wantMessages int
	}{
		{
			name: "Receive", set: func(s *fakeServerStream, err error) { s.receive = returns(err) },
			handler: func(error) connectrpc.ServerFunc { return receiveMessage }, wantMessages: 1,
		},
		{
			name: "Send", set: func(s *fakeServerStream, err error) { s.send = returns(err) },
			handler: func(error) connectrpc.ServerFunc { return sendMessage }, wantMessages: 1,
		},
		{
			name: "SendHeaders", set: func(s *fakeServerStream, err error) { s.sendHeaders = returns(err) },
			handler: func(error) connectrpc.ServerFunc {
				return func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
					return stream.SendHeaders()
				}
			},
		},
		{name: "handler", set: func(*fakeServerStream, error) {}, handler: returnsErr},
	}
	for _, op := range ops {
		for _, unsafe := range unsafeErrors {
			t.Run(op.name+"/"+unsafe.name, func(t *testing.T) {
				mt := startMockTracer(t)
				stream := &fakeServerStream{}
				op.set(stream, unsafe.err)
				var err error
				require.Equal(t, callOutcome{}, outcomeOf(t, func() {
					err = traceFakeServer(context.Background(), bidiSpec, stream, op.handler(unsafe.err))
				}))
				assert.True(t, err == unsafe.err, "got %#v, want the original error", err)
				wantError := cmp.Or(unsafe.wantError, "unsafe to inspect")
				calls := spansNamed(mt.FinishedSpans(), operationServer)
				require.Len(t, calls, 1)
				assert.Contains(t, calls[0].Tag(ext.ErrorMsg), wantError)
				messages := spansNamed(mt.FinishedSpans(), operationMessage)
				require.Len(t, messages, op.wantMessages)
				for _, span := range messages {
					assert.Contains(t, span.Tag(ext.ErrorMsg), wantError)
				}
			})
		}
	}
}

func TestStreamingHandlerMessageParent(t *testing.T) {
	for _, test := range []struct {
		name string
		opts []Option
		// ctx returns the handler's context, given a span that is not the propagated parent.
		ctx func(other *tracer.Span) context.Context
		// wantParent is "propagated", "other" or "call".
		wantParent string
		// wantKind is the message spans' span.kind, which only local roots have.
		wantKind any
	}{
		{
			name: "propagated parent without a call span", opts: []Option{WithStreamCalls(false)},
			ctx:        func(*tracer.Span) context.Context { return context.Background() },
			wantParent: "propagated", wantKind: ext.SpanKindServer,
		},
		{
			// Orchestrion's fallback makes a span discoverable without making it the explicit
			// parent, so the propagated parent, extracted afresh for every message, wins.
			name: "propagated parent beats an ambient fallback", opts: []Option{WithStreamCalls(false)},
			ctx: func(other *tracer.Span) context.Context {
				return context.WithValue(context.Background(), instr.ActiveSpanKey(), other)
			},
			wantParent: "propagated", wantKind: ext.SpanKindServer,
		},
		{
			name: "context span beats the propagated parent", opts: []Option{WithStreamCalls(false)},
			ctx:        func(other *tracer.Span) context.Context { return tracer.ContextWithSpan(context.Background(), other) },
			wantParent: "other",
		},
		{
			name:       "call span parents the messages",
			ctx:        func(*tracer.Span) context.Context { return context.Background() },
			wantParent: "call",
		},
		{
			name: "call span parents the messages despite an ambient fallback",
			ctx: func(other *tracer.Span) context.Context {
				return context.WithValue(context.Background(), instr.ActiveSpanKey(), other)
			},
			wantParent: "call",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			propagated := tracer.StartSpan("propagated")
			other := tracer.StartSpan("other")
			info := callInfoWithParent(t, connectrpc.ProtocolNameConnect, propagated)
			err := serveFake(test.ctx(other), bidiSpec, info, &fakeServerStream{}, func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
				for range 3 {
					if err := sendMessage(ctx, spec, stream); err != nil {
						return err
					}
				}
				return nil
			}, NewServerInterceptor(test.opts...))
			require.NoError(t, err)
			propagated.Finish()
			other.Finish()

			spans := mt.FinishedSpans()
			parents := map[string]*tracer.SpanContext{"propagated": propagated.Context(), "other": other.Context()}
			if calls := spansNamed(spans, operationServer); len(calls) == 1 {
				assert.Equal(t, propagated.Context().SpanID(), calls[0].ParentID())
				parents["call"] = calls[0].Unwrap().Context()
			}
			parent := parents[test.wantParent]
			require.NotNil(t, parent)
			messages := spansNamed(spans, operationMessage)
			require.Len(t, messages, 3)
			for _, span := range messages {
				assert.Equal(t, parent.TraceIDLower(), span.TraceID())
				assert.Equal(t, parent.SpanID(), span.ParentID())
				assert.Equal(t, test.wantKind, span.Tag(ext.SpanKind))
			}
		})
	}
}

func TestStreamingHandlerMessageSpans(t *testing.T) {
	// Send(nil) and SendHeaders pass through every wrapper of a handler's stream, a unary handler's
	// too, and their errors are the handler's to return.
	t.Run("Send(nil) and SendHeaders start no span", func(t *testing.T) {
		sendNil := func(s connectrpc.ServerStream) error { return s.Send(nil) }
		sendHeaders := connectrpc.ServerStream.SendHeaders
		sendHeadersErr := errors.New("send headers failed")
		for _, test := range []struct {
			name  string
			spec  connectrpc.Spec // bidiSpec if zero
			opts  []Option
			flush func(connectrpc.ServerStream) error
			// err is what the transport's Send and SendHeaders return.
			err                    error
			wantSends, wantHeaders int
			wantSpans              int
		}{
			{name: "Send(nil)", flush: sendNil, wantSends: 1, wantSpans: 1},
			{name: "SendHeaders", flush: sendHeaders, wantHeaders: 1, wantSpans: 1},
			{name: "failed SendHeaders", flush: sendHeaders, err: sendHeadersErr, wantHeaders: 1, wantSpans: 1},
			{name: "SendHeaders without a call span", opts: []Option{WithStreamCalls(false)}, flush: sendHeaders, wantHeaders: 1},
			{
				name: "failed SendHeaders without a call span", opts: []Option{WithStreamCalls(false)},
				flush: sendHeaders, err: sendHeadersErr, wantHeaders: 1,
			},
			{name: "unary SendHeaders", spec: unarySpec, flush: sendHeaders, wantHeaders: 1, wantSpans: 1},
			{name: "failed unary SendHeaders", spec: unarySpec, flush: sendHeaders, err: sendHeadersErr, wantHeaders: 1, wantSpans: 1},
			{
				name: "unary SendHeaders with request tags", spec: unarySpec, opts: []Option{WithRequestTags()},
				flush: sendHeaders, wantHeaders: 1, wantSpans: 1,
			},
			{
				name: "failed unary SendHeaders with request tags", spec: unarySpec, opts: []Option{WithRequestTags()},
				flush: sendHeaders, err: sendHeadersErr, wantHeaders: 1, wantSpans: 1,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				spec := test.spec
				if spec.Procedure == "" {
					spec = bidiSpec
				}
				var sends, headers int
				stream := &fakeServerStream{
					send:        func() error { sends++; return test.err },
					sendHeaders: func() error { headers++; return test.err },
				}
				var got error
				err := traceFakeServer(context.Background(), spec, stream, func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
					got = test.flush(stream)
					return nil
				}, test.opts...)
				require.NoError(t, err)
				assert.Equal(t, test.err, got)
				assert.Equal(t, test.wantSends, sends)
				assert.Equal(t, test.wantHeaders, headers)
				spans := mt.FinishedSpans()
				require.Len(t, spans, test.wantSpans)
				for _, span := range spans {
					assert.Equal(t, operationServer, span.OperationName())
					assert.Nil(t, span.Tag(ext.ErrorMsg))
				}
			})
		}
	})

	// The response headers set before SendHeaders reach the client over every transport.
	t.Run("SendHeaders sends the response headers", func(t *testing.T) {
		handler := func(ctx context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
			var request wrapperspb.StringValue
			if err := stream.Receive(&request); err != nil {
				return err
			}
			info, _ := connectrpc.CallInfoForServerContext(ctx)
			info.ResponseHeader().Set("X-Response", "early")
			if err := stream.SendHeaders(); err != nil {
				return err
			}
			return stream.Send(&request)
		}
		for _, transport := range testTransports {
			t.Run(transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				client := serverTracedClient(t, transport, rigOptions{
					register: func(server *connectrpc.Server) {
						server.Register(connectrpc.Method{Spec: serverStreamSpec, Handler: handler})
					},
				})
				ctx, info := connectrpc.NewClientContext(context.Background())
				values, err := callServerStream(ctx, client, "hello")
				require.NoError(t, err)
				assert.Equal(t, []string{"hello"}, values)
				assert.Equal(t, "early", info.ResponseHeader().Get("X-Response"))

				// The call span, and the message spans of Receive and Send.
				spans := waitForSpans(t, mt, 3)
				require.Len(t, spans, 3)
				assert.Len(t, spansNamed(spans, operationMessage), 2)
				for _, span := range spans {
					assert.Nil(t, span.Tag(ext.ErrorMsg), span.OperationName())
				}
			})
		}
	})

	t.Run("request tags only after a successful Receive", func(t *testing.T) {
		mt := startMockTracer(t)
		var receives int
		stream := &fakeServerStream{receive: func() error {
			if receives++; receives > 1 {
				return errors.New("receive failed")
			}
			return nil
		}}
		err := traceFakeServer(context.Background(), bidiSpec, stream, func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
			require.NoError(t, stream.Receive(wrapperspb.String("hello")))
			require.Error(t, stream.Receive(wrapperspb.String("stale")))
			return nil
		}, WithRequestTags(), WithStreamCalls(false))
		require.NoError(t, err)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		assert.Equal(t, `"hello"`, spans[0].Tag(tagConnectRequest))
		assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
		assert.Nil(t, spans[1].Tag(tagConnectRequest))
		assert.Contains(t, spans[1].Tag(ext.ErrorMsg), "receive failed")
	})

	t.Run("the end of the stream is not an error", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			err       error
			wantError bool
		}{
			{name: "io.EOF", err: io.EOF},
			{name: "coded io.EOF", err: connectrpc.NewError(connectrpc.CodeDataLoss, "truncated").WithCause(io.EOF), wantError: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				err := traceFakeServer(context.Background(), bidiSpec, &fakeServerStream{receive: returns(test.err)}, func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
					require.ErrorIs(t, receiveMessage(ctx, spec, stream), io.EOF)
					return nil
				})
				require.NoError(t, err)
				messages := spansNamed(mt.FinishedSpans(), operationMessage)
				require.Len(t, messages, 1)
				if test.wantError {
					assert.NotNil(t, messages[0].Tag(ext.ErrorMsg))
					assert.Equal(t, "data_loss", messages[0].Tag(tagConnectErrorCode))
				} else {
					assert.Nil(t, messages[0].Tag(ext.ErrorMsg))
					assert.Nil(t, messages[0].Tag(tagConnectErrorCode))
				}
			})
		}
	})
}

func TestStreamingHandlerMetadataTags(t *testing.T) {
	// The handler changes the request headers before its first message: the spans report the
	// headers that the server received, read before the handler runs, as the handler may modify
	// them concurrently with Send and Receive.
	handler := func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
		info, _ := connectrpc.CallInfoForServerContext(ctx)
		info.RequestHeader().Set("X-Meta", "changed")
		info.RequestHeader().Set("X-Late", "late")
		if err := receiveMessage(ctx, spec, stream); err != nil {
			return err
		}
		if err := receiveMessage(ctx, spec, stream); err != nil {
			return err
		}
		return sendMessage(ctx, spec, stream)
	}
	for _, test := range []struct {
		name string
		opts []Option
		// wantTagged is the finish-order index of the one span with the metadata tags: the
		// call span finishes last, so -1 is the call span.
		wantTagged int
	}{
		{name: "tags the call span", wantTagged: -1},
		{name: "without a call span, tags the first message span", opts: []Option{WithStreamCalls(false)}, wantTagged: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			info := newServerCallInfo(connectrpc.ProtocolNameConnect)
			info.RequestHeader().Set("X-Meta", "value")
			err := serveFake(context.Background(), bidiSpec, info, &fakeServerStream{}, handler, NewServerInterceptor(slices.Concat(test.opts, []Option{WithMetadataTags()})...))
			require.NoError(t, err)
			spans := mt.FinishedSpans()
			tagged := test.wantTagged
			if tagged < 0 {
				tagged += len(spans)
			}
			for i, span := range spans {
				if i == tagged {
					assert.Equal(t, "value", metadataTag(span, "x-meta"), span.OperationName())
				} else {
					assert.Nil(t, metadataTag(span, "x-meta"), "%d %s", i, span.OperationName())
				}
				assert.Nil(t, metadataTag(span, "x-late"), "%d %s", i, span.OperationName())
			}
		})
	}

	// Under -race, this fails if Send or Receive reads the CallInfo's request headers while the
	// handler's receiving goroutine modifies them.
	t.Run("headers modified while sending", func(t *testing.T) {
		const streams, messages = 3, 5
		concurrent := func(ctx context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
			info, _ := connectrpc.CallInfoForServerContext(ctx)
			var wg sync.WaitGroup
			wg.Go(func() {
				for i := 0; ; i++ {
					info.RequestHeader().Set("X-Seen-"+strconv.Itoa(i%4), "true")
					if err := stream.Receive(&wrapperspb.StringValue{}); err != nil {
						return
					}
				}
			})
			defer wg.Wait()
			for range messages {
				if err := stream.Send(wrapperspb.String("hello")); err != nil {
					return err
				}
			}
			return nil
		}
		bidi := func(t *testing.T, client *connectrpc.Client) {
			ctx, info := connectrpc.NewClientContext(context.Background())
			info.RequestHeader().Set("X-Meta", "value")
			stream, err := client.CallClientStream(ctx, bidiSpec)
			require.NoError(t, err)
			defer func() { _ = stream.Close() }()
			for range messages {
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
			}
			require.NoError(t, stream.CloseSend())
			for {
				if err := stream.Receive(&wrapperspb.StringValue{}); err != nil {
					require.ErrorIs(t, err, io.EOF)
					return
				}
			}
		}
		for _, transport := range testTransports {
			for _, callSpans := range []bool{true, false} {
				t.Run(transport.name+"/call spans "+strconv.FormatBool(callSpans), func(t *testing.T) {
					mt := startMockTracer(t)
					client := serverTracedClient(t, transport, rigOptions{
						register: func(server *connectrpc.Server) {
							server.Register(connectrpc.Method{Spec: bidiSpec, Handler: concurrent})
						},
					}, WithMetadataTags(), WithStreamCalls(callSpans))
					for range streams {
						bidi(t, client)
					}
					// Each stream has a span per message sent, and per message received plus the
					// end of the stream.
					perStream := 2*messages + 1
					if callSpans {
						perStream++
					}
					spans := waitForSpans(t, mt, streams*perStream)
					require.Len(t, spans, streams*perStream)
					var tagged int
					for _, span := range spans {
						if metadataTag(span, "x-meta") != nil {
							tagged++
						}
						assert.Empty(t, tagsWithPrefix(span, tagRequestMetadataPrefix+"x-seen-"))
					}
					assert.Equal(t, streams, tagged)
				})
			}
		}
	})
}
