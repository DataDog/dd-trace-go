// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"net"
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
	requestHeader                              func() http.Header
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
	if c.requestHeader != nil {
		return c.requestHeader()
	}
	return c.header
}

func sendHello(stream connectrpc.StreamingClientConn) { _ = stream.Send(wrapperspb.String("hello")) }

// traceFakeClient returns conn wrapped by a client interceptor configured with opts.
func traceFakeClient(ctx context.Context, conn connectrpc.StreamingClientConn, opts ...Option) connectrpc.StreamingClientConn {
	return NewClientInterceptor(opts...).WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
		return conn
	})(ctx, bidiClientSpec)
}

// streamingClientInterceptor is an interceptor that only wraps streaming clients.
type streamingClientInterceptor func(connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc

func (streamingClientInterceptor) WrapUnary(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
	return next
}

func (i streamingClientInterceptor) WrapStreamingClient(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
	return i(next)
}

func (streamingClientInterceptor) WrapStreamingHandler(next connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
	return next
}

// receiveHookConn passes what its conn's Receive returns through hook.
type receiveHookConn struct {
	connectrpc.StreamingClientConn
	hook func(error) error
}

func (c *receiveHookConn) Receive(message any) error {
	return c.hook(c.StreamingClientConn.Receive(message))
}

// closingServerURL returns the URL of an HTTP/1.1 server that reads each request up to the end of
// its chunked body, then closes the connection without responding. It stops when the test ends.
func closingServerURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				var read []byte
				buf := make([]byte, 4096)
				for !bytes.Contains(read, []byte("\r\n0\r\n\r\n")) {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					read = append(read, buf[:n]...)
				}
			}()
		}
	}()
	return "http://" + listener.Addr().String()
}

// streamingHandlerInterceptor is an interceptor that only wraps streaming handlers.
type streamingHandlerInterceptor func(connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc

func (streamingHandlerInterceptor) WrapUnary(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
	return next
}

func (streamingHandlerInterceptor) WrapStreamingClient(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
	return next
}

func (i streamingHandlerInterceptor) WrapStreamingHandler(next connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
	return i(next)
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

	// connect decodes a null detail in a Connect error body as a nil detail, which it keeps.
	t.Run("null error detail", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"not_found","message":"nope","details":[null,{"type":"google.protobuf.Duration","value":"CAE"}]}`)
		}))
		defer server.Close()
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+unaryProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor(WithErrorDetailTags())))
		var err error
		require.Equal(t, callOutcome{}, outcomeOf(t, func() {
			_, err = client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		}))
		require.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(err), "%v", err)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "not_found", spans[0].Tag(tagConnectErrorCode))
		// The tag's index is the detail's position in the body.
		assert.Equal(t, map[string]any{"connect.status_details._1": "seconds:1"}, tagsWithPrefix(spans[0], "connect.status_details."))
	})

	// The tracer calls Error while it holds the span's lock: the span records a substitute, and the
	// call still returns the original error.
	t.Run("error whose Error method panics", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		callErr := &derefError{}
		failing := connectrpc.UnaryInterceptorFunc(func(connectrpc.UnaryFunc) connectrpc.UnaryFunc {
			return func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) { return nil, callErr }
		})
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
			inMemoryHTTPClient{handler: http.NotFoundHandler()}, "http://unused"+unaryProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor(), failing))
		var err error
		require.Equal(t, callOutcome{}, outcomeOf(t, func() {
			_, err = client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		}))
		assert.Same(t, callErr, err)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "panics when formatted")
	})

	// With GODEBUG=panicnil=1, recover reports panic(nil) as no panic.
	t.Run("panic(nil)", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		mt := mocktracer.Start()
		defer mt.Stop()
		panicking := connectrpc.UnaryInterceptorFunc(func(connectrpc.UnaryFunc) connectrpc.UnaryFunc {
			return func(context.Context, connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				panicWith(nil)
				return nil, nil
			}
		})
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
			inMemoryHTTPClient{handler: http.NotFoundHandler()}, "http://unused"+unaryProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor(), panicking))
		assert.Equal(t, callOutcome{panicked: true}, outcomeOf(t, func() {
			_, _ = client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		}))
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "panic: <nil>", spans[0].Tag(ext.ErrorMsg))
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

	// The end of the context stands for an abandoned stream. A Receive that is running then and
	// returns the end of the stream shows that the caller has the RPC's result.
	for _, test := range []struct {
		name       string
		streamType connectrpc.StreamType
		// deadline ends the context with a deadline while the Receive waits for it; otherwise the
		// context is canceled while the Receive is running.
		deadline bool
		// receive is what the Receive that is running when the context ends returns.
		receive error
		// noMessage makes that Receive the first one of a client-streaming response, which then has
		// no message: CloseAndReceive fails with CodeUnimplemented, which the call span records.
		noMessage bool
		wantCode  any
		wantError bool
	}{
		{name: "end of a server stream", streamType: connectrpc.StreamTypeServer, receive: io.EOF},
		{name: "end of a server stream after the deadline", streamType: connectrpc.StreamTypeServer, deadline: true, receive: io.EOF},
		{name: "end of a bidi stream", streamType: connectrpc.StreamTypeBidi, receive: io.EOF},
		{name: "end of a client-streaming response", streamType: connectrpc.StreamTypeClient, receive: io.EOF},
		{
			name: "client-streaming response without a message", streamType: connectrpc.StreamTypeClient, noMessage: true,
			receive: io.EOF, wantCode: "unimplemented", wantError: true,
		},
		{
			name: "client-streaming response without a message after the deadline", streamType: connectrpc.StreamTypeClient,
			deadline: true, noMessage: true, receive: io.EOF, wantCode: "unimplemented", wantError: true,
		},
		{name: "server stream fails with the cancellation", streamType: connectrpc.StreamTypeServer, receive: context.Canceled, wantCode: "canceled"},
		{
			name: "server stream fails with the deadline", streamType: connectrpc.StreamTypeServer, deadline: true,
			receive: context.DeadlineExceeded, wantCode: "deadline_exceeded", wantError: true,
		},
	} {
		t.Run("the end of the response in flight when the context ends/"+test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			if test.deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
			}
			defer cancel()
			procedure := map[connectrpc.StreamType]string{
				connectrpc.StreamTypeClient: clientStreamProcedure,
				connectrpc.StreamTypeServer: serverStreamProcedure,
				connectrpc.StreamTypeBidi:   bidiProcedure,
			}[test.streamType]
			receiving, release := make(chan struct{}), make(chan struct{})
			var receives int
			conn := newFakeClientConn()
			conn.spec = func() connectrpc.Spec {
				return connectrpc.Spec{Procedure: procedure, StreamType: test.streamType, IsClient: true}
			}
			conn.receive = func() error {
				// connect reads a client-streaming response with two Receives: the second one reads
				// the end of the stream.
				if receives++; test.streamType == connectrpc.StreamTypeClient && receives == 1 && !test.noMessage {
					return nil
				}
				close(receiving)
				if test.deadline {
					<-ctx.Done()
				} else {
					<-release
				}
				return test.receive
			}
			inject := streamingClientInterceptor(func(connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
				return func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn { return conn }
			})
			// The first interceptor is the outermost, so the tracing one wraps the fake conn.
			client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](http.DefaultClient, "http://fake.invalid"+procedure,
				connectrpc.WithInterceptors(NewClientInterceptor(), inject))
			called := make(chan error, 1)
			go func() {
				switch test.streamType {
				case connectrpc.StreamTypeClient:
					stream := client.CallClientStream(ctx)
					_ = stream.Send(wrapperspb.String("hello"))
					_, err := stream.CloseAndReceive()
					called <- err
				case connectrpc.StreamTypeServer:
					stream, err := client.CallServerStream(ctx, connectrpc.NewRequest(wrapperspb.String("hello")))
					if err != nil {
						called <- err
						return
					}
					for stream.Receive() {
					}
					_ = stream.Close()
					called <- stream.Err()
				default:
					stream := client.CallBidiStream(ctx)
					_ = stream.Send(wrapperspb.String("hello"))
					_ = stream.CloseRequest()
					_, err := stream.Receive()
					_ = stream.CloseResponse()
					called <- err
				}
			}()
			if !test.deadline {
				<-receiving
				// Returns once the context watcher has started.
				cancel()
				close(release)
			}
			var err error
			requireReturns(t, "the call", func() { err = <-called })
			switch {
			case test.noMessage:
				require.Equal(t, connectrpc.CodeUnimplemented, connectrpc.CodeOf(err), "%v", err)
			case test.wantCode == nil:
				require.True(t, err == nil || errors.Is(err, io.EOF), "%v", err)
			}

			spans := mt.FinishedSpans()
			clients := spansNamed(spans, operationClient)
			require.Len(t, clients, 1)
			assert.Equal(t, test.wantCode, clients[0].Tag(tagConnectErrorCode))
			assert.Equal(t, test.wantError, clients[0].Tag(ext.ErrorMsg) != nil, "error: %v", clients[0].Tag(ext.ErrorMsg))
			if test.wantCode == nil {
				for _, span := range spansNamed(spans, operationMessage) {
					assert.Nil(t, span.Tag(ext.ErrorMsg), "message span")
				}
			}
		})
	}

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

	// assertCallError checks that span records err, or no error if err is nil, with its status.
	assertCallError := func(t *testing.T, grpc bool, span *mocktracer.Span, err error) {
		t.Helper()
		if err == nil {
			assert.Nil(t, span.Tag(ext.ErrorMsg))
		} else {
			assert.Equal(t, err.Error(), span.Tag(ext.ErrorMsg))
		}
		switch {
		case grpc && err == nil:
			assert.EqualValues(t, 0, span.Tag(tagGRPCStatusCode))
		case grpc:
			assert.EqualValues(t, connectrpc.CodeOf(err), span.Tag(tagGRPCStatusCode))
		case err == nil:
			assert.Nil(t, span.Tag(tagConnectErrorCode))
		default:
			assert.Equal(t, connectrpc.CodeOf(err).String(), span.Tag(tagConnectErrorCode))
		}
	}

	// CloseAndReceive reads the single response of a client-streaming RPC with two Receives, and
	// reports a response without a message, or with more than one, as CodeUnimplemented: the call
	// span records what it returns. A caller that reads the stream through Conn() gets the call span
	// of what its Receives returned, unless they are CloseAndReceive's. Message spans record what
	// their Receive returned.
	zeroMessages := connectrpc.NewError(connectrpc.CodeUnimplemented, errors.New("unary response has zero messages"))
	multipleMessages := connectrpc.NewError(connectrpc.CodeUnimplemented, errors.New("unary response has multiple messages"))
	lost := connectrpc.NewError(connectrpc.CodeDataLoss, errors.New("lost"))
	for _, test := range []struct {
		name      string
		responses int   // the response messages that the server sends
		end       error // the error that the server ends the RPC with
		// codeEOF makes an inner interceptor code every error of the traced conn's Receive as
		// unavailable, the end of the stream included, as an interceptor that translates errors
		// would.
		codeEOF bool
		// viaConn reads the response through Conn() until a Receive fails, instead of with
		// CloseAndReceive.
		viaConn bool
		// wantErr is what the call span records, and what CloseAndReceive returns.
		wantErr error
	}{
		{name: "no response message", wantErr: zeroMessages},
		{name: "extra response message", responses: 2, wantErr: multipleMessages},
		{name: "extra response message before an error", responses: 2, end: lost, wantErr: multipleMessages},
		{name: "error after the response message", responses: 1, end: lost, wantErr: lost},
		{name: "no response message with a coded end of stream", codeEOF: true, wantErr: zeroMessages},
		{name: "coded end of stream after the response message", responses: 1, codeEOF: true},
		// A first Receive that returns the end of the stream is CloseAndReceive's.
		{name: "Conn() reader/no response message", viaConn: true, wantErr: zeroMessages},
		{name: "Conn() reader/extra response message", responses: 2, viaConn: true},
		{name: "Conn() reader/extra response message before an error", responses: 2, end: lost, viaConn: true, wantErr: lost},
		{name: "Conn() reader/error after the response message", responses: 1, end: lost, viaConn: true, wantErr: lost},
		{name: "Conn() reader/no response message with a coded end of stream", codeEOF: true, viaConn: true, wantErr: zeroMessages},
		// connect's end of stream is any error that wraps io.EOF.
		{name: "Conn() reader/coded end of stream after the response message", responses: 1, codeEOF: true, viaConn: true},
	} {
		for _, protocol := range testProtocols {
			t.Run(test.name+"/"+protocol.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				respond := streamingHandlerInterceptor(func(connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
					return func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
						for conn.Receive(&wrapperspb.StringValue{}) == nil {
						}
						for range test.responses {
							if err := conn.Send(wrapperspb.String("response")); err != nil {
								return err
							}
						}
						return test.end
					}
				})
				mux := http.NewServeMux()
				mux.Handle(clientStreamProcedure, connectrpc.NewClientStreamHandler(clientStreamProcedure, clientStreamHandler,
					connectrpc.WithInterceptors(respond)))
				// received is what the traced conn's Receives return.
				var received []error
				inner := streamingClientInterceptor(func(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
					return func(ctx context.Context, spec connectrpc.Spec) connectrpc.StreamingClientConn {
						return &receiveHookConn{StreamingClientConn: next(ctx, spec), hook: func(err error) error {
							if err != nil && test.codeEOF {
								err = connectrpc.NewError(connectrpc.CodeUnavailable, err)
							}
							received = append(received, err)
							return err
						}}
					}
				})
				rig := &testRig{server: newTLSServer(t, mux), protocolOpts: append([]connectrpc.ClientOption{connectrpc.WithInterceptors(inner)}, protocol.opts...)}

				stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				if test.viaConn {
					conn, err := stream.Conn()
					require.NoError(t, err)
					require.NoError(t, conn.CloseRequest())
					for conn.Receive(&wrapperspb.StringValue{}) == nil {
					}
					_ = conn.CloseResponse()
				} else {
					_, err := stream.CloseAndReceive()
					if test.wantErr == nil {
						require.NoError(t, err)
					} else {
						require.EqualError(t, err, test.wantErr.Error())
						require.Equal(t, connectrpc.CodeOf(test.wantErr), connectrpc.CodeOf(err))
					}
				}

				spans := mt.FinishedSpans()
				calls := spansNamed(spans, operationClient)
				require.Len(t, calls, 1)
				assertCallError(t, protocol.grpc, calls[0], test.wantErr)
				// One message span per Send and per Receive, in that order.
				messages := spansNamed(spans, operationMessage)
				require.Len(t, messages, 1+len(received))
				assert.Nil(t, messages[0].Tag(ext.ErrorMsg), "Send")
				for i, err := range received {
					if err == nil || (errors.Is(err, io.EOF) && connectrpc.CodeOf(err) == connectrpc.CodeUnknown) {
						assert.Nil(t, messages[1+i].Tag(ext.ErrorMsg), "Receive %d", i+1)
					} else {
						assert.Equal(t, err.Error(), messages[1+i].Tag(ext.ErrorMsg), "Receive %d", i+1)
					}
				}
			})
		}
	}

	// connect reports a response that never comes, because the server closes the connection, as an
	// unavailable error that wraps io.ErrUnexpectedEOF. Before v1.17 it wrapped io.EOF, the shape
	// the coded end-of-stream rows above cover.
	for _, protocol := range testProtocols {
		if protocol.name == "grpc" {
			// gRPC requires HTTP/2.
			continue
		}
		t.Run("connection closed without a response/"+protocol.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](&http.Client{Transport: transport},
				closingServerURL(t)+clientStreamProcedure, append([]connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor())}, protocol.opts...)...)
			var err error
			requireReturns(t, "the call", func() {
				stream := client.CallClientStream(context.Background())
				_ = stream.Send(wrapperspb.String("hello"))
				_, err = stream.CloseAndReceive()
			})
			require.Error(t, err)
			calls := spansNamed(mt.FinishedSpans(), operationClient)
			require.Len(t, calls, 1)
			assertCallError(t, protocol.grpc, calls[0], err)
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
	errorPanics := &derefError{}
	connectEOF := connectrpc.NewError(connectrpc.CodeUnknown, io.EOF)
	receive := func(stream connectrpc.StreamingClientConn) { _ = stream.Receive(nil) }
	for _, test := range []struct {
		name string
		opts func() []Option
		conn func(*fakeClientConn) // nil if creating the connection panics with wantPanic
		op   func(connectrpc.StreamingClientConn)
		// unknownProtocol is set if the panic happens before the conn reports its protocol: the call
		// span then has none of the protocol's tags.
		unknownProtocol bool
		wantPanic       any
		// panicNil runs the case with GODEBUG=panicnil=1, with which recover reports a panic(nil),
		// such as the one wantPanic then stands for, as no panic.
		panicNil  bool
		wantSpans int
	}{
		{name: "creating the connection", unknownProtocol: true, wantPanic: "constructor panic", wantSpans: 1},
		{
			name: "creating the connection with an error that panics when unwrapped", unknownProtocol: true,
			wantPanic: wrappedDerefError, wantSpans: 1,
		},
		{
			name: "reading the peer", unknownProtocol: true, wantPanic: "peer panic", wantSpans: 1,
			conn: func(c *fakeClientConn) { c.peer = func() connectrpc.Peer { panic("peer panic") } },
		},
		{
			name: "reading the request header", wantPanic: "header panic", wantSpans: 1,
			conn: func(c *fakeClientConn) { c.requestHeader = func() http.Header { panic("header panic") } },
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
		// A Send that returns connect's end-of-stream signal doesn't end the stream, but a panic with
		// it does.
		{name: "Send with io.EOF", conn: func(c *fakeClientConn) { c.send = panics(io.EOF) }, op: sendHello, wantPanic: io.EOF, wantSpans: 2},
		{
			name: "Send with connect's io.EOF", conn: func(c *fakeClientConn) { c.send = panics(connectEOF) },
			op: sendHello, wantPanic: connectEOF, wantSpans: 2,
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
		{
			name: "creating the connection with an error whose Error method panics", unknownProtocol: true,
			wantPanic: errorPanics, wantSpans: 1,
		},
		{
			name: "Receive with an error whose Error method panics", conn: func(c *fakeClientConn) { c.receive = panics(errorPanics) },
			op: receive, wantPanic: errorPanics, wantSpans: 2,
		},
		{name: "creating the connection with nil", unknownProtocol: true, panicNil: true, wantSpans: 1},
		{name: "Receive with nil", conn: func(c *fakeClientConn) { c.receive = panics(nil) }, op: receive, panicNil: true, wantSpans: 2},
		{
			name: "error check with nil", op: sendHello, panicNil: true, wantSpans: 2,
			opts: func() []Option {
				return []Option{WithErrorCheck(func(string, error) bool { panicWith(nil); return true })}
			},
			conn: func(c *fakeClientConn) { c.send = returns(connectrpc.NewError(connectrpc.CodeInternal, io.EOF)) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
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
			wantSystem, wantCode := any("connectrpc"), any("unknown")
			if test.unknownProtocol {
				wantSystem, wantCode = nil, nil
			}
			assert.Equal(t, wantSystem, calls[0].Tag(ext.RPCSystem))
			assert.Equal(t, wantCode, calls[0].Tag(tagConnectErrorCode))
		})
	}
}

// TestClientUnknownProtocol checks that a call whose connection reports no protocol has none of the
// tags that name one, and so no peer.service derived from rpc.service, but records its error.
func TestClientUnknownProtocol(t *testing.T) {
	failure := connectrpc.NewError(connectrpc.CodeUnavailable, errors.New("circuit open"))
	for _, test := range []struct {
		name string
		// open opens the connection in place of connect, unless it is nil.
		open      connectrpc.StreamingClientFunc
		wantPanic any
	}{
		{name: "known protocol"},
		{
			name: "opening panics", wantPanic: "open panic",
			open: func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn { panic("open panic") },
		},
		{
			name: "a connection that an interceptor returns",
			open: func(_ context.Context, spec connectrpc.Spec) connectrpc.StreamingClientConn {
				conn := newFakeClientConn()
				conn.spec = func() connectrpc.Spec { return spec }
				conn.peer = func() connectrpc.Peer { return connectrpc.Peer{} }
				conn.send, conn.closeRequest, conn.receive = returns(failure), returns(failure), returns(failure)
				return conn
			},
		},
	} {
		for _, protocol := range testProtocols {
			t.Run(test.name+"/"+protocol.name, func(t *testing.T) {
				tr, agent, err := tracertest.Bootstrap(t, tracer.WithPeerServiceDefaults(true), tracer.WithLogger(testutils.DiscardLogger()))
				require.NoError(t, err)
				mux := http.NewServeMux()
				mux.Handle(clientStreamProcedure, connectrpc.NewClientStreamHandler(clientStreamProcedure, clientStreamHandler))
				opts := slices.Clone(protocol.opts)
				if test.open != nil {
					open := streamingClientInterceptor(func(connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc { return test.open })
					opts = append(opts, connectrpc.WithInterceptors(open))
				}
				rig := &testRig{server: newTLSServer(t, mux), protocolOpts: opts}
				o := outcomeOf(t, func() {
					stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
					_ = stream.Send(wrapperspb.String("hello"))
					_, _ = stream.CloseAndReceive()
				})
				assert.Equal(t, callOutcome{panicked: test.wantPanic != nil, value: test.wantPanic}, o)
				tr.Flush()

				spans := agent.(agenttest.SpanLister).Spans()
				var call *agenttest.Span
				for _, span := range spans {
					if span.Operation == operationClient {
						require.Nil(t, call, "more than one call span")
						call = span
					}
				}
				require.NotNil(t, call)
				if test.open == nil {
					assert.Equal(t, protocol.system, call.Meta[ext.RPCSystem])
					assert.Equal(t, "test.connect.v1.TestService", call.Meta[ext.PeerService])
					return
				}
				for _, key := range []string{ext.RPCSystem, ext.GRPCFullMethod, tagConnectErrorCode, tagGRPCStatusCode, ext.PeerService} {
					assert.NotContains(t, call.Tags, key)
				}
				assert.EqualValues(t, 1, call.Error)
				assert.NotEmpty(t, call.Meta[ext.ErrorMsg])
				for _, span := range spans {
					if span.Operation == operationMessage {
						assert.NotContains(t, span.Tags, ext.RPCSystem, "message span")
					}
				}
			})
		}
	}
}

// TestStreamingClientUnsafeErrors checks errors whose methods panic because of a nil pointer.
func TestStreamingClientUnsafeErrors(t *testing.T) {
	ops := []struct {
		name string
		// spec is the stream's, bidiClientSpec if zero.
		spec connectrpc.Spec
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
			// A client-streaming Receive counts the responses (see endReceive).
			name: "client-streaming Receive", spec: connectrpc.Spec{Procedure: clientStreamProcedure, StreamType: connectrpc.StreamTypeClient, IsClient: true},
			set:  func(c *fakeClientConn, err error) { c.receive = returns(err) },
			call: func(stream connectrpc.StreamingClientConn) error { return stream.Receive(nil) },
		},
		{
			name: "CloseRequest", set: func(c *fakeClientConn, err error) { c.closeRequest = returns(err) },
			call: func(stream connectrpc.StreamingClientConn) error { return stream.CloseRequest() },
		},
	}
	for _, op := range ops {
		for _, unsafe := range unsafeErrors {
			t.Run(op.name+"/"+unsafe.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				conn := newFakeClientConn()
				op.set(conn, unsafe.err)
				spec := op.spec
				if spec.Procedure == "" {
					spec = bidiClientSpec
				}
				stream := NewClientInterceptor().WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
					return conn
				})(context.Background(), spec)
				var err error
				require.Equal(t, callOutcome{}, outcomeOf(t, func() { err = op.call(stream) }))
				assert.True(t, err == unsafe.err, "got %#v, want the connection's own error", err)
				requireReturns(t, "CloseResponse", func() { _ = stream.CloseResponse() })
				wantError := cmp.Or(unsafe.wantError, "unsafe to inspect")
				calls := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, calls, 1)
				assert.Contains(t, calls[0].Tag(ext.ErrorMsg), wantError)
				for _, span := range spansNamed(mt.FinishedSpans(), operationMessage) {
					assert.Contains(t, span.Tag(ext.ErrorMsg), wantError)
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
		reads := conn.headerCalls.Load()
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		assert.Equal(t, reads+1, conn.headerCalls.Load(), "Send reads the metadata")
		assert.Nil(t, metadataTag(mt.FinishedSpans()[0], "x-late"))
	})
}
