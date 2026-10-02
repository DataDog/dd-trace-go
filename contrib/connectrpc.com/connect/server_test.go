// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fakeHandlerConn is a StreamingHandlerConn whose Receive and Send can be replaced. By default,
// both succeed.
type fakeHandlerConn struct {
	header        http.Header
	receive, send func() error
	// headerPanicsAfter, if positive, is the number of RequestHeader calls after which
	// RequestHeader panics.
	headerPanicsAfter int32
	headerCalls       atomic.Int32
}

func newFakeHandlerConn() *fakeHandlerConn {
	return &fakeHandlerConn{header: make(http.Header)}
}

func (*fakeHandlerConn) Spec() connectrpc.Spec { return bidiSpec }

func (*fakeHandlerConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "127.0.0.1:1234", Protocol: connectrpc.ProtocolConnect}
}

func (c *fakeHandlerConn) Receive(any) error { return callOr(c.receive, nil) }
func (c *fakeHandlerConn) Send(any) error    { return callOr(c.send, nil) }

func (c *fakeHandlerConn) RequestHeader() http.Header {
	if calls := c.headerCalls.Add(1); c.headerPanicsAfter > 0 && calls > c.headerPanicsAfter {
		panic("header panic")
	}
	return c.header
}

func (*fakeHandlerConn) ResponseHeader() http.Header  { return make(http.Header) }
func (*fakeHandlerConn) ResponseTrailer() http.Header { return make(http.Header) }

// traceFakeHandler runs handler on conn through a server interceptor configured with opts.
func traceFakeHandler(ctx context.Context, conn connectrpc.StreamingHandlerConn, handler connectrpc.StreamingHandlerFunc, opts ...Option) error {
	return NewServerInterceptor(opts...).WrapStreamingHandler(handler)(ctx, conn)
}

func receiveMessage(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
	return conn.Receive(&wrapperspb.StringValue{})
}

func sendMessage(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
	return conn.Send(wrapperspb.String("hello"))
}

func TestUnaryHandler(t *testing.T) {
	var typedNilConnectErr *connectrpc.Error
	for _, test := range []struct {
		name      string
		next      connectrpc.UnaryFunc
		wantPanic any
		wantError string // a substring of the recorded error; empty if none may be recorded
	}{
		{
			name: "success",
			next: func(_ context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				return connectrpc.NewResponse(request.Any().(*wrapperspb.StringValue)), nil
			},
		},
		{
			name: "panic", wantPanic: "handler panic", wantError: "handler panic",
			next: func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) { panic("handler panic") },
		},
		{
			name: "typed nil error", wantError: "unsafe to inspect",
			next: func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				return nil, typedNilConnectErr
			},
		},
		{
			name: "error that panics when unwrapped", wantError: "unsafe to inspect",
			next: func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				return nil, wrappedDerefError
			},
		},
		{
			name: "panic with an error that panics when unwrapped", wantPanic: wrappedDerefError, wantError: "unsafe to inspect",
			next: func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) { panic(wrappedDerefError) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			parent := tracer.StartSpan("parent")
			// A request that connect did not build is a handler's: its spec is not a client's.
			request := connectrpc.NewRequest(wrapperspb.String("hello"))
			require.NoError(t, tracer.Inject(parent.Context(), tracer.HTTPHeadersCarrier(request.Header())))
			handle := func() { _, _ = NewServerInterceptor().WrapUnary(test.next)(context.Background(), request) }
			if test.wantPanic != nil {
				assert.PanicsWithValue(t, test.wantPanic, handle)
			} else {
				assert.NotPanics(t, handle)
			}
			parent.Finish()

			spans := spansNamed(mt.FinishedSpans(), operationServer)
			require.Len(t, spans, 1)
			span := spans[0]
			assert.Equal(t, parent.Context().SpanID(), span.ParentID())
			assert.Equal(t, "server", span.Tag(ext.SpanKind))
			assert.Equal(t, "connectrpc", span.Tag(ext.RPCSystem))
			if test.wantError == "" {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, span.Tag(ext.ErrorMsg), test.wantError)
			}
		})
	}
}

// TestStreamingHandlerPanics checks that a panic finishes every span with an error and is
// re-raised unchanged.
func TestStreamingHandlerPanics(t *testing.T) {
	for _, test := range []struct {
		name      string
		opts      []Option
		conn      func(*fakeHandlerConn)
		handler   connectrpc.StreamingHandlerFunc
		wantPanic any
		wantSpans int
	}{
		{
			name: "handler", wantPanic: "handler panic", wantSpans: 1,
			handler: func(context.Context, connectrpc.StreamingHandlerConn) error { panic("handler panic") },
		},
		{name: "Receive", conn: func(c *fakeHandlerConn) { c.receive = panics("receive panic") }, handler: receiveMessage, wantPanic: "receive panic", wantSpans: 2},
		{name: "Send", conn: func(c *fakeHandlerConn) { c.send = panics("send panic") }, handler: sendMessage, wantPanic: "send panic", wantSpans: 2},
		{
			name: "Receive with an error that panics when unwrapped", conn: func(c *fakeHandlerConn) { c.receive = panics(wrappedDerefError) },
			handler: receiveMessage, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Send with an error that panics when unwrapped", conn: func(c *fakeHandlerConn) { c.send = panics(wrappedDerefError) },
			handler: sendMessage, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			// The first RequestHeader call extracts the parent, the second tags the metadata.
			name: "tagging metadata on the call span", opts: []Option{WithMetadataTags(), WithStreamMessages(false)},
			conn: func(c *fakeHandlerConn) { c.headerPanicsAfter = 1 }, handler: receiveMessage, wantPanic: "header panic", wantSpans: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			conn := newFakeHandlerConn()
			if test.conn != nil {
				test.conn(conn)
			}
			assert.PanicsWithValue(t, test.wantPanic, func() {
				_ = traceFakeHandler(context.Background(), conn, test.handler, test.opts...)
			})
			spans := mt.FinishedSpans()
			require.Len(t, spans, test.wantSpans)
			for _, span := range spans {
				assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
			}
		})
	}
}

// TestStreamingHandlerUnsafeErrors checks errors whose methods panic because of a nil pointer.
func TestStreamingHandlerUnsafeErrors(t *testing.T) {
	var typedNilConnectErr *connectrpc.Error
	errs := []struct {
		name string
		err  error
	}{
		{name: "typed nil connect error", err: typedNilConnectErr},
		{name: "typed nil custom error", err: (*derefError)(nil)},
		{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}},
		{name: "wrapped typed nil connect error", err: fmt.Errorf("op: %w", typedNilConnectErr)},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
		{name: "wrapped typed nil error whose Is method panics", err: wrappedDerefIsError},
	}
	ops := []struct {
		name         string
		set          func(*fakeHandlerConn, error)
		handler      func(error) connectrpc.StreamingHandlerFunc
		wantMessages int
	}{
		{
			name: "Receive", set: func(c *fakeHandlerConn, err error) { c.receive = returns(err) },
			handler: func(error) connectrpc.StreamingHandlerFunc { return receiveMessage }, wantMessages: 1,
		},
		{
			name: "Send", set: func(c *fakeHandlerConn, err error) { c.send = returns(err) },
			handler: func(error) connectrpc.StreamingHandlerFunc { return sendMessage }, wantMessages: 1,
		},
		{
			name: "handler", set: func(*fakeHandlerConn, error) {},
			handler: func(err error) connectrpc.StreamingHandlerFunc {
				return func(context.Context, connectrpc.StreamingHandlerConn) error { return err }
			},
		},
	}
	for _, op := range ops {
		for _, unsafe := range errs {
			t.Run(op.name+"/"+unsafe.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				conn := newFakeHandlerConn()
				op.set(conn, unsafe.err)
				var err error
				require.NotPanics(t, func() {
					err = traceFakeHandler(context.Background(), conn, op.handler(unsafe.err))
				})
				assert.True(t, err == unsafe.err, "got %#v, want the original error", err)
				calls := spansNamed(mt.FinishedSpans(), operationServer)
				require.Len(t, calls, 1)
				assert.Contains(t, calls[0].Tag(ext.ErrorMsg), "unsafe to inspect")
				messages := spansNamed(mt.FinishedSpans(), operationMessage)
				require.Len(t, messages, op.wantMessages)
				for _, span := range messages {
					assert.Contains(t, span.Tag(ext.ErrorMsg), "unsafe to inspect")
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
			// Orchestrion's fallback makes a span discoverable without making it the explicit
			// parent, so the propagated parent, extracted afresh for every message, wins.
			name: "propagated parent beats an ambient fallback", opts: []Option{WithStreamCalls(false)},
			ctx: func(other *tracer.Span) context.Context {
				return context.WithValue(context.Background(), instr.ActiveSpanKey(), other)
			},
			wantParent: "propagated", wantKind: "server",
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
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			propagated := tracer.StartSpan("propagated")
			other := tracer.StartSpan("other")
			conn := newFakeHandlerConn()
			require.NoError(t, tracer.Inject(propagated.Context(), tracer.HTTPHeadersCarrier(conn.header)))
			err := traceFakeHandler(test.ctx(other), conn, func(ctx context.Context, conn connectrpc.StreamingHandlerConn) error {
				for range 3 {
					if err := sendMessage(ctx, conn); err != nil {
						return err
					}
				}
				return nil
			}, test.opts...)
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
	t.Run("Send(nil) flushes the headers without a message span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		err := traceFakeHandler(context.Background(), newFakeHandlerConn(), func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
			return conn.Send(nil)
		})
		require.NoError(t, err)
		require.Len(t, mt.FinishedSpans(), 1)
		assert.Equal(t, operationServer, mt.FinishedSpans()[0].OperationName())
	})

	t.Run("request tags only after a successful Receive", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		conn := newFakeHandlerConn()
		var receives atomic.Int32
		conn.receive = func() error {
			if receives.Add(1) > 1 {
				return errors.New("receive failed")
			}
			return nil
		}
		err := traceFakeHandler(context.Background(), conn, func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
			require.NoError(t, conn.Receive(wrapperspb.String("hello")))
			require.Error(t, conn.Receive(wrapperspb.String("stale")))
			return nil
		}, WithRequestTags(), WithStreamCalls(false))
		require.NoError(t, err)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		assert.Equal(t, `"hello"`, spans[0].Tag(tagConnectRequest))
		assert.Nil(t, spans[1].Tag(tagConnectRequest))
	})
}

func TestStreamingHandlerMetadataTags(t *testing.T) {
	handler := func(ctx context.Context, conn connectrpc.StreamingHandlerConn) error {
		if err := receiveMessage(ctx, conn); err != nil {
			return err
		}
		if err := receiveMessage(ctx, conn); err != nil {
			return err
		}
		return sendMessage(ctx, conn)
	}
	for _, test := range []struct {
		name string
		opts []Option
		// wantTagged is the finish-order index of the one span with the metadata tags: the
		// call span finishes last, so -1 is the call span.
		wantTagged int
		// wantHeaderReads counts RequestHeader calls: Send and Receive must not make any, as the
		// handler may modify the headers concurrently.
		wantHeaderReads int32
	}{
		{name: "tags the call span", wantTagged: -1, wantHeaderReads: 2},
		{name: "without a call span, tags the first message span", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantHeaderReads: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			conn := newFakeHandlerConn()
			conn.header.Set("X-Meta", "value")
			require.NoError(t, traceFakeHandler(context.Background(), conn, handler, append(test.opts, WithMetadataTags())...))
			assert.Equal(t, test.wantHeaderReads, conn.headerCalls.Load())
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
			}
		})
	}

	// Under -race, this fails if Send reads the request headers while the handler's receiving
	// goroutine modifies them.
	t.Run("headers modified while sending", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		const messages = 5
		concurrent := func(_ context.Context, stream *connectrpc.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
			var wg sync.WaitGroup
			wg.Go(func() {
				for i := 0; ; i++ {
					stream.RequestHeader().Set("X-Seen-"+strconv.Itoa(i%4), "true")
					if _, err := stream.Receive(); err != nil {
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
		mux := http.NewServeMux()
		mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, concurrent,
			connectrpc.WithInterceptors(NewServerInterceptor(WithMetadataTags(), WithStreamCalls(false)))))
		server := newTLSServer(t, mux)
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+bidiProcedure)
		for range 10 {
			stream := client.CallBidiStream(context.Background())
			stream.RequestHeader().Set("X-Meta", "value")
			for range messages {
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
			}
			require.NoError(t, stream.CloseRequest())
			for {
				if _, err := stream.Receive(); err != nil {
					require.ErrorIs(t, err, io.EOF)
					break
				}
			}
			require.NoError(t, stream.CloseResponse())
		}
		// Each stream has a span per message sent, and per message received plus the end of
		// the stream.
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 10*(2*messages+1) }, 5*time.Second, time.Millisecond)
		var tagged int
		for _, span := range mt.FinishedSpans() {
			if metadataTag(span, "x-meta") != nil {
				tagged++
			}
			assert.Nil(t, metadataTag(span, "x-seen-0"))
		}
		assert.Equal(t, 10, tagged)
	})
}
