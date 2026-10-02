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
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fakeClientConn is a StreamingClientConn whose methods can be replaced. By default, Send and
// the Close methods succeed and Receive reports the end of the stream.
type fakeClientConn struct {
	header                                     http.Header
	headerCalls                                atomic.Int32
	spec                                       func() connectrpc.Spec
	peer                                       func() connectrpc.Peer
	send, receive, closeRequest, closeResponse func() error
}

func newFakeClientConn() *fakeClientConn {
	return &fakeClientConn{header: make(http.Header)}
}

func (c *fakeClientConn) Spec() connectrpc.Spec {
	if c.spec != nil {
		return c.spec()
	}
	return bidiClientSpec
}

func (c *fakeClientConn) Peer() connectrpc.Peer {
	if c.peer != nil {
		return c.peer()
	}
	return connectrpc.Peer{Addr: "localhost:8080", Protocol: connectrpc.ProtocolConnect}
}

func (c *fakeClientConn) Send(any) error             { return callOr(c.send, nil) }
func (c *fakeClientConn) Receive(any) error          { return callOr(c.receive, io.EOF) }
func (c *fakeClientConn) CloseRequest() error        { return callOr(c.closeRequest, nil) }
func (c *fakeClientConn) CloseResponse() error       { return callOr(c.closeResponse, nil) }
func (*fakeClientConn) ResponseHeader() http.Header  { return make(http.Header) }
func (*fakeClientConn) ResponseTrailer() http.Header { return make(http.Header) }

func (c *fakeClientConn) RequestHeader() http.Header {
	c.headerCalls.Add(1)
	return c.header
}

func sendHello(stream connectrpc.StreamingClientConn) { _ = stream.Send(wrapperspb.String("hello")) }

// traceFakeClient returns conn wrapped by a client interceptor configured with opts.
func traceFakeClient(ctx context.Context, conn connectrpc.StreamingClientConn, opts ...Option) connectrpc.StreamingClientConn {
	return NewClientInterceptor(opts...).WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
		return conn
	})(ctx, bidiClientSpec)
}

// newTestStreamingClientConn wraps conn around span without the interceptor, so that a test can
// drive the stream's bookkeeping directly.
func newTestStreamingClientConn(cfg *config, conn connectrpc.StreamingClientConn, span *tracer.Span) *streamingClientConn {
	peer := conn.Peer()
	return newStreamingClientConn(context.Background(), cfg, conn, span, conn.Spec(), protocolTagsFor(peer.Protocol), newPeerTags(peer))
}

func TestUnaryClient(t *testing.T) {
	t.Run("GET not modified", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mux := http.NewServeMux()
		mux.Handle(unaryProcedure, connectrpc.NewUnaryHandler(unaryProcedure,
			func(context.Context, *connectrpc.Request[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
				return nil, connectrpc.NewNotModifiedError(nil)
			},
			connectrpc.WithIdempotency(connectrpc.IdempotencyNoSideEffects),
			connectrpc.WithInterceptors(NewServerInterceptor())))
		server := httptest.NewServer(mux)
		defer server.Close()
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+unaryProcedure,
			connectrpc.WithHTTPGet(),
			connectrpc.WithIdempotency(connectrpc.IdempotencyNoSideEffects),
			connectrpc.WithInterceptors(NewClientInterceptor()))

		_, err := client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		require.True(t, connectrpc.IsNotModifiedError(err), "%v", err)

		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			assert.Equal(t, "304", span.Tag(ext.HTTPCode), "%v", span.Tags())
			assert.Nil(t, span.Tag(ext.ErrorMsg))
			assert.Nil(t, span.Tag(tagConnectErrorCode))
		}
	})

	t.Run("panic", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		panicking := connectrpc.UnaryInterceptorFunc(func(connectrpc.UnaryFunc) connectrpc.UnaryFunc {
			return func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				panic("unary panic")
			}
		})
		// The first interceptor is the outermost, so the tracing one sees the panic.
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
			inMemoryHTTPClient{handler: http.NotFoundHandler()}, "http://unused"+unaryProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor(), panicking))
		assert.PanicsWithValue(t, "unary panic", func() {
			_, _ = client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		})
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "connect.client", spans[0].OperationName())
		assert.Equal(t, "connectrpc", spans[0].Tag(ext.RPCSystem))
		assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "unary panic")
	})
}

func TestStreamingClientCallSpan(t *testing.T) {
	t.Run("finishes when the stream ends", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithStreamMessages(false)}
		rig := newTestRig(t, opts, opts)
		stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
		require.NoError(t, stream.Send(wrapperspb.String("error")))
		assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "the call span finished before the stream ended")
		_, err := stream.CloseAndReceive()
		require.Error(t, err)
		clientSpan, _ := requireCallPair(t, mt, methodKindClientStream)
		assert.Equal(t, "internal", clientSpan.Tag(tagConnectErrorCode))
		assert.NotNil(t, clientSpan.Tag(ext.ErrorMsg))
	})

	t.Run("finishes when the context is canceled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithStreamMessages(false)}
		rig := newTestRig(t, opts, opts)
		ctx, cancel := context.WithCancel(context.Background())
		_ = rig.client(bidiProcedure).CallBidiStream(ctx)
		cancel()
		require.Eventually(t, func() bool {
			return len(spansNamed(mt.FinishedSpans(), operationClient)) == 1
		}, 5*time.Second, time.Millisecond)
		span := spansNamed(mt.FinishedSpans(), operationClient)[0]
		assert.Equal(t, "canceled", span.Tag(tagConnectErrorCode))
		assert.Nil(t, span.Tag(ext.ErrorMsg))
	})

	// With the span pool, a finished call span's trace may already be flushed and its spans
	// reused, so a late operation must not start a message span under it.
	t.Run("no message spans after the call span has finished", func(t *testing.T) {
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithSpanPool(true), tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		mux := http.NewServeMux()
		mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, bidiHandler))
		server := newTLSServer(t, mux)
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+bidiProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor()))
		// Starts other spans, which reuse pooled ones, while the streams run.
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := 1; ; i++ {
				select {
				case <-done:
					return
				default:
					tracer.StartSpan("other").Finish()
					// The tracer drops whole traces once its 1000-trace queue is full.
					if i%256 == 0 {
						tr.Flush()
					}
				}
			}
		})
		for range 10 {
			stream := client.CallBidiStream(context.Background())
			require.NoError(t, stream.Send(wrapperspb.String("error")))
			_, err := stream.Receive()
			require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err))
			_ = stream.Send(wrapperspb.String("late"))
			_, _ = stream.Receive()
			require.NoError(t, stream.CloseResponse())
		}
		close(done)
		wg.Wait()
		tr.Flush()
		counts := map[string]int{}
		agent.FindSpan(agenttest.With().Condition("count", func(span *agenttest.Span) bool {
			counts[span.Operation]++
			return false
		}))
		require.Equal(t, 10, counts[operationClient])
		// Each stream's first Send and Receive.
		assert.Equal(t, 20, counts[operationMessage])
	})

	// connect reports a stream the server has ended as an io.EOF from Send, and the server's
	// error from Receive.
	for _, protocol := range testProtocols {
		t.Run("server fails first/"+protocol.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			const procedure = "/test.connect.v1.TestService/FailFast"
			mux := http.NewServeMux()
			mux.Handle(procedure, connectrpc.NewClientStreamHandler(procedure,
				func(context.Context, *connectrpc.ClientStream[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
					return nil, connectrpc.NewError(connectrpc.CodeNotFound, errors.New("nope"))
				},
				connectrpc.WithInterceptors(NewServerInterceptor())))
			server := newTLSServer(t, mux)
			client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+procedure,
				append([]connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor())}, protocol.opts...)...)

			stream := client.CallClientStream(context.Background())
			_ = stream.Send(wrapperspb.String("hello")) // starts the request
			conn, err := stream.Conn()
			require.NoError(t, err)
			// ResponseHeader returns once the server has ended the RPC; Send fails once the
			// transport has processed the end of the stream.
			conn.ResponseHeader()
			var sendErr error
			require.Eventually(t, func() bool {
				sendErr = stream.Send(wrapperspb.String("hello"))
				return sendErr != nil
			}, 5*time.Second, time.Millisecond)
			require.ErrorIs(t, sendErr, io.EOF)
			assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "Send's io.EOF finished the call span")
			_, err = stream.CloseAndReceive()
			require.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(err))

			clientSpan, _ := requireCallPair(t, mt, methodKindClientStream)
			if protocol.grpc {
				assert.EqualValues(t, connectrpc.CodeNotFound, clientSpan.Tag(tagGRPCStatusCode))
			} else {
				assert.Equal(t, "not_found", clientSpan.Tag(tagConnectErrorCode))
			}
			assert.Contains(t, clientSpan.Tag(ext.ErrorMsg), "nope")
		})
	}

	sentinelEOF := connectrpc.NewError(connectrpc.CodeUnknown, io.EOF)
	codedEOF := connectrpc.NewError(connectrpc.CodeInternal, io.EOF)
	nope := connectrpc.NewError(connectrpc.CodeNotFound, errors.New("nope"))
	unavailable := connectrpc.NewError(connectrpc.CodeUnavailable, errors.New("unavailable"))
	for _, test := range []struct {
		name string
		conn func(*fakeClientConn)
		// run returns the errors of the operations it runs, which must be the conn's own.
		run       func(connectrpc.StreamingClientConn) []error
		wantErrs  []error
		wantCode  any
		wantError string // a substring of the recorded error; empty if none may be recorded
	}{
		{
			name:     "end of stream",
			run:      func(s connectrpc.StreamingClientConn) []error { return []error{s.Receive(nil)} },
			wantErrs: []error{io.EOF},
		},
		{
			name: "Send EOF defers to Receive", conn: func(c *fakeClientConn) { c.send, c.receive = returns(sentinelEOF), returns(nope) },
			run: func(s connectrpc.StreamingClientConn) []error {
				return []error{s.Send(wrapperspb.String("hello")), s.Receive(nil)}
			},
			wantErrs: []error{sentinelEOF, nope}, wantCode: "not_found", wantError: "nope",
		},
		{
			name: "coded EOF from Send", conn: func(c *fakeClientConn) { c.send = returns(codedEOF) },
			run:      func(s connectrpc.StreamingClientConn) []error { return []error{s.Send(wrapperspb.String("hello"))} },
			wantErrs: []error{codedEOF}, wantCode: "internal", wantError: "EOF",
		},
		{
			name: "coded EOF from Receive", conn: func(c *fakeClientConn) { c.receive = returns(codedEOF) },
			run:      func(s connectrpc.StreamingClientConn) []error { return []error{s.Receive(nil)} },
			wantErrs: []error{codedEOF}, wantCode: "internal", wantError: "EOF",
		},
		{
			name: "CloseRequest error", conn: func(c *fakeClientConn) { c.closeRequest = returns(unavailable) },
			run:      func(s connectrpc.StreamingClientConn) []error { return []error{s.CloseRequest()} },
			wantErrs: []error{unavailable}, wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name:     "CloseResponse",
			run:      func(s connectrpc.StreamingClientConn) []error { return []error{s.CloseResponse()} },
			wantErrs: []error{nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			conn := newFakeClientConn()
			if test.conn != nil {
				test.conn(conn)
			}
			errs := test.run(traceFakeClient(context.Background(), conn, WithStreamMessages(false)))
			assert.Equal(t, test.wantErrs, errs)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, test.wantCode, spans[0].Tag(tagConnectErrorCode))
			if test.wantError == "" {
				assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, spans[0].Tag(ext.ErrorMsg), test.wantError)
			}
		})
	}

	// A panic while recovering from a panic in a conn method would replace the original panic.
	t.Run("reads the spec and peer only once", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		var peerCalls atomic.Int32
		conn := newFakeClientConn()
		conn.spec = func() connectrpc.Spec { panic("spec panic") }
		conn.peer = func() connectrpc.Peer {
			if peerCalls.Add(1) > 1 {
				panic("peer panic")
			}
			return connectrpc.Peer{Addr: "localhost:8080", Protocol: connectrpc.ProtocolConnect}
		}
		conn.receive = returns(nil)
		stream := traceFakeClient(context.Background(), conn)
		assert.NotPanics(t, func() { _ = stream.Send(wrapperspb.String("hello")) })
		assert.NotPanics(t, func() { _ = stream.Receive(&wrapperspb.StringValue{}) })
		assert.NotPanics(t, func() { _ = stream.CloseRequest() })
		assert.NotPanics(t, func() { _ = stream.CloseResponse() })
		assert.Len(t, spansNamed(mt.FinishedSpans(), operationClient), 1)
		assert.Len(t, spansNamed(mt.FinishedSpans(), operationMessage), 2)
	})
}

// TestStreamingClientTerminalError drives the bookkeeping of streams whose operations and context
// race to end the stream, and checks which error the call span records.
func TestStreamingClientTerminalError(t *testing.T) {
	canceled := func(message string) error { return connectrpc.NewError(connectrpc.CodeCanceled, errors.New(message)) }
	internal := func(message string) error { return connectrpc.NewError(connectrpc.CodeInternal, errors.New(message)) }
	var (
		rejected  = internal("flaky, ignore")
		rejected2 = internal("flaky again")
		accepted  = internal("real failure")
		shadowed  = internal("shadowed")
	)
	rejects := func(errs ...error) func(int32, error) bool {
		return func(_ int32, err error) bool { return !slices.Contains(errs, err) }
	}
	for _, test := range []struct {
		name string
		// errCheck is called with the number of calls so far, including this one.
		errCheck   func(calls int32, err error) bool
		noCallSpan bool
		run        func(*streamingClientConn)
		wantCode   any
		// wantError is a substring of the recorded error; empty if none may be recorded.
		wantError      string
		wantCheckCalls int32 // checked if positive, or with noCallSpan
	}{
		{
			name: "real error beats an earlier cancellation",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.requestFinish(context.Canceled)
				c.endOperation(connectrpc.NewError(connectrpc.CodeUnavailable, errors.New("boom")), true, false)
			},
			wantCode: "unavailable", wantError: "boom",
		},
		{
			name: "panic displaces a stored error",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(canceled("canceled"), true, false)
				c.endOperation(errors.New("boom"), true, true)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name: "panic is never displaced",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(errors.New("boom"), true, true)
				c.endOperation(internal("later"), true, false)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name: "real error replaces a suppressed one",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(canceled("send canceled"), true, false)
				c.endOperation(internal("boom"), true, false)
			},
			wantCode: "internal", wantError: "boom",
		},
		{
			// Were the rejected error only a fallback, the status would stay canceled.
			name:     "rejected real error still replaces a suppressed one",
			errCheck: rejects(rejected),
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(canceled("send canceled"), true, false)
				c.endOperation(rejected, true, false)
			},
			wantCode: "internal", wantCheckCalls: 1,
		},
		{
			name: "context deadline replaces a suppressed error",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(canceled("send canceled"), true, false)
				c.requestFinish(context.DeadlineExceeded)
				// The in-flight operation ends without ending the stream itself.
				c.endOperation(nil, false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name: "context deadline survives a later suppressed error",
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.requestFinish(context.DeadlineExceeded)
				c.endOperation(canceled("receive canceled"), true, false)
				c.endOperation(nil, false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name:     "error check runs once for a suppressed then a real error",
			errCheck: func(int32, error) bool { return true },
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(canceled("send canceled"), true, false)
				c.endOperation(internal("boom"), true, false)
			},
			wantCode: "internal", wantError: "boom", wantCheckCalls: 1,
		},
		{
			name:     "falls back when the error check rejects the first real error",
			errCheck: rejects(rejected),
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(rejected, true, false)
				c.endOperation(accepted, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 2,
		},
		{
			name:     "keeps the first real error when the error check rejects the second",
			errCheck: rejects(rejected),
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(accepted, true, false)
				c.endOperation(rejected, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 1,
		},
		{
			// A stateful check would flip its verdict if the primary error were checked twice.
			name:     "error check runs once for the primary error",
			errCheck: func(calls int32, _ error) bool { return calls == 1 },
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(accepted, true, false)
				c.endOperation(shadowed, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 1,
		},
		{
			name:     "three-way race keeps the accepted fallback",
			errCheck: rejects(rejected, rejected2),
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(rejected, true, false)
				c.requestFinish(context.DeadlineExceeded)
				c.endOperation(rejected2, true, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name:     "error check rejecting every error keeps the status",
			errCheck: rejects(rejected, rejected2),
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.beginOperation()
				c.endOperation(rejected, true, false)
				c.endOperation(rejected2, true, false)
			},
			wantCode: "internal", wantCheckCalls: 2,
		},
		{
			name:       "no call span skips classification",
			errCheck:   func(int32, error) bool { return true },
			noCallSpan: true,
			run: func(c *streamingClientConn) {
				c.beginOperation()
				c.endOperation(internal("boom"), true, false)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			var opts []Option
			if test.errCheck != nil {
				opts = append(opts, WithErrorCheck(func(_ string, err error) bool {
					return test.errCheck(calls.Add(1), err)
				}))
			}
			var span *tracer.Span
			if !test.noCallSpan {
				span = tracer.StartSpan("connect.client")
			}
			conn := newTestStreamingClientConn(newConfig(opts...), newFakeClientConn(), span)
			require.NotPanics(t, func() { test.run(conn) })
			if test.wantCheckCalls > 0 || test.noCallSpan {
				assert.Equal(t, test.wantCheckCalls, calls.Load(), "error check calls")
			}
			if test.noCallSpan {
				assert.Empty(t, mt.FinishedSpans())
				return
			}
			require.Len(t, mt.FinishedSpans(), 1)
			finished := mt.FinishedSpans()[0]
			assert.Equal(t, test.wantCode, finished.Tag(tagConnectErrorCode))
			if test.wantError == "" {
				assert.Nil(t, finished.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, finished.Tag(ext.ErrorMsg), test.wantError)
			}
		})
	}

	// An application may keep using a stream whose call span has finished.
	t.Run("errors after the finish are not kept", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		conn := newTestStreamingClientConn(newConfig(), newFakeClientConn(), tracer.StartSpan("connect.client"))
		conn.beginOperation()
		conn.endOperation(internal("first"), true, false)
		require.Len(t, mt.FinishedSpans(), 1)
		for range 100 {
			conn.beginOperation()
			conn.endOperation(internal("later"), true, false)
		}
		conn.mu.Lock()
		defer conn.mu.Unlock()
		assert.Empty(t, conn.fallbacks)
		assert.Equal(t, internal("first").Error(), conn.terminal.err.Error())
	})
}

func TestStreamingClientContextWatcher(t *testing.T) {
	t.Run("an operation ending the stream waits for a running watcher", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		stopCalled := make(chan struct{})
		conn := newTestStreamingClientConn(newConfig(), newFakeClientConn(), tracer.StartSpan("connect.client"))
		conn.ctxFinishDone = make(chan struct{})
		// What context.AfterFunc's stop returns once the context is done and the watcher has
		// started.
		conn.stopContextDone = func() bool {
			close(stopCalled)
			return false
		}
		conn.beginOperation()

		endOperationDone := make(chan struct{})
		go func() {
			defer close(endOperationDone)
			// Finishing with this suppressed error without waiting for the watcher would record
			// no error.
			conn.endOperation(connectrpc.NewError(connectrpc.CodeCanceled, errors.New("send canceled")), true, false)
		}()
		select {
		case <-stopCalled:
		case <-time.After(5 * time.Second):
			t.Fatal("endOperation did not stop the context watcher")
		}
		select {
		case <-endOperationDone:
			t.Fatal("endOperation did not wait for the context watcher")
		case <-time.After(20 * time.Millisecond):
		}
		conn.requestFinish(context.DeadlineExceeded)
		select {
		case <-endOperationDone:
		case <-time.After(5 * time.Second):
			t.Fatal("endOperation did not resume after the context watcher returned")
		}

		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		assert.Equal(t, "deadline_exceeded", finished.Tag(tagConnectErrorCode))
		assert.NotNil(t, finished.Tag(ext.ErrorMsg))
	})

	// The watcher runs in its own goroutine, where a panic would crash the process.
	t.Run("recovers an error check panic", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := newConfig(WithErrorCheck(func(string, error) bool { panic("errCheck panic") }))
		conn := newTestStreamingClientConn(cfg, newFakeClientConn(), tracer.StartSpan("connect.client"))
		require.NotPanics(t, func() {
			conn.requestFinish(connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom")))
		})
		require.Len(t, mt.FinishedSpans(), 1)
		assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
	})
}

func TestStreamingClientErrorCheckReentry(t *testing.T) {
	boom := connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom"))
	for _, test := range []struct {
		name string
		conn func(*fakeClientConn)
		// expired makes the stream's context done from the start, so the watcher ends it.
		expired bool
		run     func(connectrpc.StreamingClientConn)
	}{
		{
			name: "terminal Receive", conn: func(c *fakeClientConn) { c.receive = returns(boom) },
			run: func(stream connectrpc.StreamingClientConn) { _ = stream.Receive(nil) },
		},
		{name: "terminal Send", conn: func(c *fakeClientConn) { c.send = returns(boom) }, run: sendHello},
		{name: "context watcher", expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var stream connectrpc.StreamingClientConn
			ready, reentered := make(chan struct{}), make(chan struct{})
			var once sync.Once
			errCheck := WithErrorCheck(func(string, error) bool {
				<-ready
				_ = stream.CloseResponse()
				once.Do(func() { close(reentered) })
				return true
			})
			ctx := context.Background()
			if test.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
			}
			conn := newFakeClientConn()
			if test.conn != nil {
				test.conn(conn)
			}
			stream = traceFakeClient(ctx, conn, WithStreamMessages(false), errCheck)
			close(ready)
			if test.run != nil {
				go test.run(stream)
			}
			select {
			case <-reentered:
			case <-time.After(5 * time.Second):
				t.Fatal("the error check reentering the stream deadlocked")
			}
			require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
			assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
		})
	}
}

// TestStreamingClientPanics checks that a panic finishes every span with an error and is
// re-raised unchanged.
func TestStreamingClientPanics(t *testing.T) {
	for _, test := range []struct {
		name      string
		opts      func() []Option
		conn      func(*fakeClientConn) // nil if creating the connection panics with wantPanic
		op        func(connectrpc.StreamingClientConn)
		wantPanic any
		wantSpans int
	}{
		{name: "creating the connection", wantPanic: "constructor panic", wantSpans: 1},
		{name: "creating the connection with an error that panics when unwrapped", wantPanic: wrappedDerefError, wantSpans: 1},
		{
			name: "reading the peer", wantPanic: "peer panic", wantSpans: 1,
			conn: func(c *fakeClientConn) { c.peer = func() connectrpc.Peer { panic("peer panic") } },
		},
		{name: "Send", conn: func(c *fakeClientConn) { c.send = panics("send panic") }, op: sendHello, wantPanic: "send panic", wantSpans: 2},
		{
			name: "Receive", conn: func(c *fakeClientConn) { c.receive = panics("receive panic") },
			op: func(stream connectrpc.StreamingClientConn) { _ = stream.Receive(nil) }, wantPanic: "receive panic", wantSpans: 2,
		},
		{
			name: "Receive with an error that panics when unwrapped", conn: func(c *fakeClientConn) { c.receive = panics(wrappedDerefError) },
			op: func(stream connectrpc.StreamingClientConn) { _ = stream.Receive(nil) }, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Send with an error that panics when unwrapped", conn: func(c *fakeClientConn) { c.send = panics(wrappedDerefError) },
			op: sendHello, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Receive with io.EOF", opts: func() []Option { return []Option{WithStreamMessages(false)} },
			conn: func(c *fakeClientConn) { c.receive = panics(io.EOF) },
			op:   func(stream connectrpc.StreamingClientConn) { _ = stream.Receive(nil) }, wantPanic: io.EOF, wantSpans: 1,
		},
		{
			name: "CloseRequest", conn: func(c *fakeClientConn) { c.closeRequest = panics("close request panic") },
			op: func(stream connectrpc.StreamingClientConn) { _ = stream.CloseRequest() }, wantPanic: "close request panic", wantSpans: 1,
		},
		{
			name: "CloseResponse", conn: func(c *fakeClientConn) { c.closeResponse = panics("close response panic") },
			op: func(stream connectrpc.StreamingClientConn) { _ = stream.CloseResponse() }, wantPanic: "close response panic", wantSpans: 1,
		},
		{
			// The option runs for the call span first, then panics for the message span.
			name: "starting a message span", conn: func(*fakeClientConn) {}, op: sendHello, wantPanic: "span option panic", wantSpans: 1,
			opts: func() []Option {
				var calls int
				return []Option{WithSpanOptions(func(*tracer.StartSpanConfig) {
					if calls++; calls > 1 {
						panic("span option panic")
					}
				})}
			},
		},
		{
			name: "error check", op: sendHello, wantPanic: "errCheck panic", wantSpans: 2,
			opts: func() []Option { return []Option{WithErrorCheck(func(string, error) bool { panic("errCheck panic") })} },
			conn: func(c *fakeClientConn) { c.send = returns(connectrpc.NewError(connectrpc.CodeInternal, io.EOF)) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var opts []Option
			if test.opts != nil {
				opts = test.opts()
			}
			newConn := NewClientInterceptor(opts...).WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
				if test.conn == nil {
					panic(test.wantPanic)
				}
				conn := newFakeClientConn()
				test.conn(conn)
				return conn
			})
			assert.PanicsWithValue(t, test.wantPanic, func() {
				stream := newConn(context.Background(), bidiClientSpec)
				if test.op != nil {
					test.op(stream)
				}
			})
			spans := mt.FinishedSpans()
			require.Len(t, spans, test.wantSpans)
			for _, span := range spans {
				assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
			}
			calls := spansNamed(spans, operationClient)
			require.Len(t, calls, 1)
			// Even when the connection, and so the protocol, is unknown.
			assert.Equal(t, "connectrpc", calls[0].Tag(ext.RPCSystem))
		})
	}
}

// TestStreamingClientUnsafeErrors checks errors whose methods panic because of a nil pointer.
func TestStreamingClientUnsafeErrors(t *testing.T) {
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
		name string
		set  func(*fakeClientConn, error)
		call func(connectrpc.StreamingClientConn) error
	}{
		{
			name: "Send", set: func(c *fakeClientConn, err error) { c.send = returns(err) },
			call: func(stream connectrpc.StreamingClientConn) error { return stream.Send(wrapperspb.String("hello")) },
		},
		{
			name: "Receive", set: func(c *fakeClientConn, err error) { c.receive = returns(err) },
			call: func(stream connectrpc.StreamingClientConn) error { return stream.Receive(nil) },
		},
		{
			name: "CloseRequest", set: func(c *fakeClientConn, err error) { c.closeRequest = returns(err) },
			call: func(stream connectrpc.StreamingClientConn) error { return stream.CloseRequest() },
		},
	}
	for _, op := range ops {
		for _, unsafe := range errs {
			t.Run(op.name+"/"+unsafe.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				conn := newFakeClientConn()
				op.set(conn, unsafe.err)
				stream := traceFakeClient(context.Background(), conn)
				var err error
				require.NotPanics(t, func() { err = op.call(stream) })
				assert.True(t, err == unsafe.err, "got %#v, want the connection's own error", err)
				requireReturns(t, "CloseResponse", func() { _ = stream.CloseResponse() })
				calls := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, calls, 1)
				assert.Contains(t, calls[0].Tag(ext.ErrorMsg), "unsafe to inspect")
				for _, span := range spansNamed(mt.FinishedSpans(), operationMessage) {
					assert.Contains(t, span.Tag(ext.ErrorMsg), "unsafe to inspect")
				}
			})
		}
	}
}

func TestStreamingClientMetadataTags(t *testing.T) {
	t.Run("tags the call spans, not the message spans", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithMetadataTags()}
		rig := newTestRig(t, opts, opts)
		stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
		// Headers set after the stream is created are still sent, so they are tagged.
		stream.RequestHeader().Set("X-Late-Header", "value")
		runBidi(t, stream)
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindBidiStream)
		assert.Equal(t, "value", metadataTag(clientSpan, "x-late-header"))
		assert.Equal(t, "value", metadataTag(serverSpan, "x-late-header"))
		messages := spansNamed(mt.FinishedSpans(), operationMessage)
		require.Len(t, messages, 6)
		for _, span := range messages {
			assert.Nil(t, metadataTag(span, "x-late-header"))
		}
	})

	t.Run("Send(nil) flushes the headers without a message span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		rig := newTestRig(t, []Option{WithService("server")}, []Option{WithService("client"), WithMetadataTags()})
		stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
		stream.RequestHeader().Set("X-Flush", "value")
		require.NoError(t, stream.Send(nil))
		require.NoError(t, stream.CloseRequest())
		_, err := stream.Receive()
		require.ErrorIs(t, err, io.EOF)
		require.NoError(t, stream.CloseResponse())

		clientSpan, _ := requireCallPair(t, mt, methodKindBidiStream)
		assert.Equal(t, "value", metadataTag(clientSpan, "x-flush"))
		var clientMessages int
		for _, span := range spansNamed(mt.FinishedSpans(), operationMessage) {
			if span.Tag(ext.ServiceName) == "client" {
				clientMessages++
				assert.Nil(t, metadataTag(span, "x-flush"))
			}
		}
		assert.Equal(t, 1, clientMessages, "only the final Receive creates a client message span")
	})

	// Only the write side reads the metadata: Receive may run concurrently with the application
	// setting it, or with net/http adding cookies once the first Send has started the request.
	for _, test := range []struct {
		name string
		opts []Option
		// run runs the stream's operations, calling setHeader after the first one.
		run func(stream connectrpc.StreamingClientConn, setHeader func())
		// wantTagged is the finish-order index of the one span with the metadata, or -1 for the
		// call span; wantValue tells whether it was read before or after setHeader.
		wantTagged int
		wantValue  string
	}{
		{
			name: "Receive first, then Send", wantTagged: -1, wantValue: "late",
			run: func(stream connectrpc.StreamingClientConn, setHeader func()) {
				_ = stream.Receive(nil)
				setHeader()
				sendHello(stream)
				_ = stream.CloseResponse()
			},
		},
		{
			name: "without a call span, Receive first, then Send", opts: []Option{WithStreamCalls(false)}, wantTagged: 1, wantValue: "late",
			run: func(stream connectrpc.StreamingClientConn, setHeader func()) {
				_ = stream.Receive(nil)
				setHeader()
				sendHello(stream)
			},
		},
		{
			// The value the write side saw is kept, even if the headers change afterwards.
			name: "without a call span, Send(nil), then Receive", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantValue: "early",
			run: func(stream connectrpc.StreamingClientConn, setHeader func()) {
				_ = stream.Send(nil)
				setHeader()
				_ = stream.Receive(nil)
				_ = stream.Receive(nil)
			},
		},
		{
			name: "without a call span, CloseRequest, then Receive", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantValue: "early",
			run: func(stream connectrpc.StreamingClientConn, setHeader func()) {
				_ = stream.CloseRequest()
				setHeader()
				_ = stream.Receive(nil)
				_ = stream.Receive(nil)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			conn := newFakeClientConn()
			conn.header.Set("X-Meta", "early")
			conn.receive = returns(nil)
			stream := traceFakeClient(context.Background(), conn, append(test.opts, WithMetadataTags())...)
			created := conn.headerCalls.Load()
			test.run(stream, func() { conn.header.Set("X-Meta", "late") })
			assert.Equal(t, created+1, conn.headerCalls.Load(), "metadata reads")
			spans := mt.FinishedSpans()
			tagged := test.wantTagged
			if tagged < 0 {
				tagged += len(spans)
			}
			for i, span := range spans {
				if i == tagged {
					assert.Equal(t, test.wantValue, metadataTag(span, "x-meta"), "%d %s", i, span.OperationName())
				} else {
					assert.Nil(t, metadataTag(span, "x-meta"), "%d %s", i, span.OperationName())
				}
			}
		})
	}

	// Under -race, these fail if the interceptor reads the metadata from Receive.
	for _, mode := range []struct {
		name string
		opts []Option
	}{
		{name: "call span"},
		{name: "message spans", opts: []Option{WithStreamCalls(false)}},
	} {
		for _, test := range []struct {
			name string
			jar  bool
		}{
			{name: "cookie jar", jar: true},
			{name: "header set after Receive started"},
		} {
			t.Run("read on the write side/"+mode.name+"/"+test.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				mux := http.NewServeMux()
				mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, bidiHandler))
				server := newTLSServer(t, mux)
				httpClient := server.Client()
				if test.jar {
					jar, err := cookiejar.New(nil)
					require.NoError(t, err)
					serverURL, err := url.Parse(server.URL)
					require.NoError(t, err)
					jar.SetCookies(serverURL, []*http.Cookie{{Name: "session", Value: "secret"}})
					httpClient.Jar = jar
				}
				client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](httpClient, server.URL+bidiProcedure,
					connectrpc.WithInterceptors(NewClientInterceptor(append(mode.opts, WithMetadataTags())...)))
				for range 20 {
					stream := client.CallBidiStream(context.Background())
					var wg sync.WaitGroup
					started := make(chan struct{})
					wg.Go(func() {
						close(started)
						for {
							if _, err := stream.Receive(); err != nil {
								assert.ErrorIs(t, err, io.EOF)
								return
							}
						}
					})
					<-started
					stream.RequestHeader().Set("X-Meta", "value")
					require.NoError(t, stream.Send(wrapperspb.String("hello")))
					require.NoError(t, stream.CloseRequest())
					wg.Wait()
					require.NoError(t, stream.CloseResponse())

					var tagged int
					for _, span := range mt.FinishedSpans() {
						if metadataTag(span, "x-meta") != nil {
							tagged++
						}
					}
					assert.Equal(t, 1, tagged)
					mt.Reset()
				}
			})
		}
	}

	t.Run("without a call span, tags the first message span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		conn := newFakeClientConn()
		conn.header.Set("X-Meta", "value")
		stream := traceFakeClient(context.Background(), conn, WithMetadataTags(), WithStreamCalls(false))
		require.NoError(t, stream.Send(nil))
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		require.ErrorIs(t, stream.Receive(nil), io.EOF)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 3, "Send(nil) creates no message span")
		assert.Equal(t, "value", metadataTag(spans[0], "x-meta"))
		for _, span := range spans[1:] {
			assert.Nil(t, metadataTag(span, "x-meta"))
		}
	})

	t.Run("not after the context ends the stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		ctx, cancel := context.WithCancel(context.Background())
		conn := newFakeClientConn()
		conn.header.Set("X-Late", "value")
		stream := traceFakeClient(ctx, conn, WithMetadataTags(), WithStreamMessages(false))
		cancel()
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		wrapped, ok := stream.(*streamingClientConn)
		require.True(t, ok)
		assert.True(t, wrapped.finishClaimed.Load())
		assert.True(t, wrapped.metadataRead.Load())
		assert.Nil(t, metadataTag(mt.FinishedSpans()[0], "x-late"))
	})

	// The tracer may reuse a finished span for another trace, so the stream must not tag it even
	// though the span object is live again.
	t.Run("never tags a finished call span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		conn := newFakeClientConn()
		conn.header.Set("X-Late", "value")
		span := tracer.StartSpan("reused")
		stream := newTestStreamingClientConn(newConfig(WithMetadataTags(), WithStreamMessages(false)), conn, span)
		stream.finishClaimed.Store(true)
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		span.Finish()
		require.Len(t, mt.FinishedSpans(), 1)
		assert.Nil(t, metadataTag(mt.FinishedSpans()[0], "x-late"))
	})
}
