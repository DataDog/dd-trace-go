// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"

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

func TestUnaryClient(t *testing.T) {
	t.Run("GET not modified", func(t *testing.T) {
		mt := startMockTracer(t)
		rig := newTestRig(t, connectTransport, rigOptions{
			serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()},
			transportOptions:   []connecthttp.Option{connecthttp.WithHTTPGet()},
		})
		_, err := callUnary(context.Background(), rig.client(NewClientInterceptor()), valueNotModified)
		require.True(t, connecthttp.IsNotModifiedError(err), "%v", err)

		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			side := span.Tag(ext.SpanKind)
			assert.Equal(t, "304", span.Tag(ext.HTTPCode), "%s: %v", side, span.Tags())
			assert.Nil(t, span.Tag(ext.ErrorMsg), side)
			assert.Nil(t, span.Tag(tagConnectErrorCode), side)
		}
	})

	// Only a Connect GET is answered with a 304. connectinprocess hands the server's not-modified
	// error to the client as is, so only the protocol tells it apart.
	for _, test := range []struct {
		name      string
		transport testTransport
		options   []connecthttp.Option
	}{
		{name: "connect POST", transport: connectTransport},
		{name: "grpc", transport: grpcTransport, options: []connecthttp.Option{connecthttp.WithHTTPGet()}},
		{name: "grpcweb", transport: grpcWebTransport, options: []connecthttp.Option{connecthttp.WithHTTPGet()}},
		{name: "inprocess", transport: inProcessTransport},
	} {
		t.Run("not modified is an error/"+test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			rig := newTestRig(t, test.transport, rigOptions{
				serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()},
				transportOptions:   test.options,
			})
			_, err := callUnary(context.Background(), rig.client(NewClientInterceptor()), valueNotModified)
			require.Error(t, err)
			require.Equal(t, connectrpc.CodeUnknown, connectrpc.CodeOf(err), "%v", err)
			assert.Equal(t, test.transport.inProcess, connecthttp.IsNotModifiedError(err))

			clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
			for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
				side := span.Tag(ext.SpanKind)
				assert.Nil(t, span.Tag(ext.HTTPCode), side)
				assert.NotNil(t, span.Tag(ext.ErrorMsg), side)
				assertStatus(t, test.transport, span, connectrpc.CodeUnknown)
			}
		})
	}

	// The protocol is only tagged once the transport has reported it: before, the call span has
	// none of its tags.
	for _, test := range []struct {
		name string
		// open makes the panicking interceptor open the stream first.
		open       bool
		wantSystem any
	}{
		{name: "panic before the transport"},
		{name: "panic after the transport", open: true, wantSystem: ext.RPCSystemGRPC},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			panicking := func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
				return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
					if test.open {
						_, _ = next(ctx, spec)
					}
					panic("unary panic")
				}
			}
			transport := fakeTransport(fakeClientFunc(&fakeClientStream{}, connectrpc.ProtocolNameGRPC, fakePeerAddr))
			// The first interceptor is the outermost, so the tracing one sees the panic.
			client := connectrpc.NewClient(transport, NewClientInterceptor(), panicking)
			assert.PanicsWithValue(t, "unary panic", func() {
				_, _ = callUnary(context.Background(), client, "hello")
			})
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, operationClient, spans[0].OperationName())
			assert.Equal(t, methodKindUnary, spans[0].Tag(tagMethodKind))
			assert.Equal(t, test.wantSystem, spans[0].Tag(ext.RPCSystem))
			if test.wantSystem == nil {
				assert.Nil(t, spans[0].Tag(tagConnectErrorCode))
				assert.Nil(t, spans[0].Tag(tagGRPCStatusCode))
			}
			assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "unary panic")
		})
	}

	// connecthttp sends the request of a unary call from Send, which fails once its deadline has
	// expired. The context watcher may finish the call span first, on its own goroutine, so the span
	// may finish after the call returns.
	t.Run("deadline expired at Send", func(t *testing.T) {
		for _, transport := range []testTransport{connectTransport, grpcTransport, grpcWebTransport} {
			t.Run(transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, nil, nil)
				ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
				defer cancel()
				_, err := callUnary(ctx, client, "hello")
				require.Equal(t, connectrpc.CodeDeadlineExceeded, connectrpc.CodeOf(err), "%v", err)

				clients := spansNamed(waitForSpans(t, mt, 1), operationClient)
				require.Len(t, clients, 1)
				assertStatus(t, transport, clients[0], connectrpc.CodeDeadlineExceeded)
				assert.Contains(t, clients[0].Tag(ext.ErrorMsg), "deadline")
			})
		}
	})
}

func TestStreamingClientCallSpan(t *testing.T) {
	for _, transport := range testTransports {
		t.Run("finishes when the stream ends/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			opts := []Option{WithStreamMessages(false)}
			_, client := newTracedRig(t, transport, opts, opts)
			stream, err := client.CallClientStream(context.Background(), clientStreamSpec)
			require.NoError(t, err)
			require.NoError(t, stream.Send(wrapperspb.String(valueError)))
			assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "the call span finished before the stream ended")
			require.NoError(t, stream.CloseSend())
			err = stream.Receive(&wrapperspb.StringValue{})
			require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)

			clientSpan, _ := requireCallPair(t, mt, methodKindClientStream)
			assertStatus(t, transport, clientSpan, connectrpc.CodeInternal)
			assert.NotNil(t, clientSpan.Tag(ext.ErrorMsg))
		})

		t.Run("finishes when the context is canceled/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			opts := []Option{WithStreamMessages(false)}
			_, client := newTracedRig(t, transport, opts, opts)
			ctx, cancel := context.WithCancel(context.Background())
			stream, err := client.CallClientStream(ctx, bidiSpec)
			require.NoError(t, err)
			defer func() { _ = stream.Close() }()
			cancel()
			require.Eventually(t, func() bool {
				return len(spansNamed(mt.FinishedSpans(), operationClient)) == 1
			}, 5*time.Second, time.Millisecond)
			span := spansNamed(mt.FinishedSpans(), operationClient)[0]
			assertStatus(t, transport, span, connectrpc.CodeCanceled)
			assert.Nil(t, span.Tag(ext.ErrorMsg))
		})
	}

	// A client-streaming call ends at its first Receive, which reads its only response: the
	// generated CloseAndReceive never calls Close.
	t.Run("client stream ends at its first Receive", func(t *testing.T) {
		for _, transport := range testTransports {
			t.Run(transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, nil, nil)
				response, err := callClientStream(context.Background(), client, "hello", "world")
				require.NoError(t, err)
				assert.Equal(t, "world", response.Value)
				clients := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, clients, 1, "the call span is finished once the call returns")
				assert.Nil(t, clients[0].Tag(ext.ErrorMsg))
				assert.Len(t, spansNamed(childrenOf(mt.FinishedSpans(), clients[0]), operationMessage), 3, "two Sends and one Receive")
			})
		}

		// When an operation still in progress keeps the call span open, the io.EOF of a later Receive
		// is the end of the stream, not a missing response.
		t.Run("later Receive", func(t *testing.T) {
			mt := startMockTracer(t)
			sending, release := make(chan struct{}), make(chan struct{})
			var receives atomic.Int32
			stream := &fakeClientStream{
				send: func() error {
					close(sending)
					<-release
					return nil
				},
				receive: func() error {
					if receives.Add(1) == 1 {
						return nil
					}
					return io.EOF
				},
			}
			client := connectrpc.NewClient(fakeTransport(fakeClientFunc(stream, connectrpc.ProtocolNameConnect, fakePeerAddr)), NewClientInterceptor())
			traced, err := client.CallClientStream(context.Background(), clientStreamSpec)
			require.NoError(t, err)
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				sendHello(traced)
			}()
			<-sending
			require.NoError(t, traced.Receive(&wrapperspb.StringValue{}))
			require.ErrorIs(t, traced.Receive(&wrapperspb.StringValue{}), io.EOF)
			assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "the call span finished while Send was running")
			close(release)
			<-sent

			spans := mt.FinishedSpans()
			clients := spansNamed(spans, operationClient)
			require.Len(t, clients, 1)
			assert.Nil(t, clients[0].Tag(ext.ErrorMsg))
			assert.Nil(t, clients[0].Tag(tagConnectErrorCode))
			messages := spansNamed(childrenOf(spans, clients[0]), operationMessage)
			assert.Len(t, messages, 3, "a Send and two Receives")
			for _, span := range messages {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			}
		})

		// The call span has finished, as for the generated CloseAndReceive, when a caller reads past
		// the only response: connecthttp fails the first Receive if the server sent more than one or
		// none, and then returns io.EOF, but connectinprocess returns what the server sent, then its
		// error. The call span records what the first Receive returned in both cases.
		lost := connectrpc.NewError(connectrpc.CodeDataLoss, "lost")
		for _, test := range []struct {
			name      string
			responses int
			end       error
			// wantInProcess is what the Receives return over connectinprocess, and wantHTTP over
			// connecthttp: a response, the end of the stream, or an error's code.
			wantInProcess, wantHTTP []string
		}{
			{
				name: "an error after the response", responses: 1, end: lost,
				wantInProcess: []string{"response", "data_loss"}, wantHTTP: []string{"data_loss"},
			},
			{
				name: "a second response", responses: 2,
				wantInProcess: []string{"response", "response", "EOF"}, wantHTTP: []string{"unimplemented"},
			},
			{
				name: "an error after a second response", responses: 2, end: lost,
				wantInProcess: []string{"response", "response", "data_loss"}, wantHTTP: []string{"unimplemented"},
			},
		} {
			for _, transport := range testTransports {
				t.Run("reading past the response, "+test.name+"/"+transport.name, func(t *testing.T) {
					mt := startMockTracer(t)
					rig := newTestRig(t, transport, rigOptions{register: func(server *connectrpc.Server) {
						server.Register(connectrpc.Method{Spec: clientStreamSpec, Handler: func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
							for stream.Receive(&wrapperspb.StringValue{}) == nil {
							}
							for range test.responses {
								if err := stream.Send(wrapperspb.String("response")); err != nil {
									return err
								}
							}
							return test.end
						}})
					}})
					stream, err := rig.client(NewClientInterceptor()).CallClientStream(context.Background(), clientStreamSpec)
					require.NoError(t, err)
					require.NoError(t, stream.Send(wrapperspb.String("hello")))
					require.NoError(t, stream.CloseSend())
					var receives []string
					var first error
					for {
						var response wrapperspb.StringValue
						err := stream.Receive(&response)
						if receives == nil {
							first = err
						}
						result := response.Value
						if errors.Is(err, io.EOF) {
							result = "EOF"
						} else if err != nil {
							result = connectrpc.CodeOf(err).String()
						}
						receives = append(receives, result)
						if err != nil {
							break
						}
					}
					if transport.inProcess {
						require.Equal(t, test.wantInProcess, receives)
					} else {
						require.Equal(t, test.wantHTTP, receives)
					}

					spans := mt.FinishedSpans()
					clients := spansNamed(spans, operationClient)
					require.Len(t, clients, 1)
					if first == nil {
						assert.Nil(t, clients[0].Tag(ext.ErrorMsg))
						assertStatus(t, transport, clients[0], 0)
					} else {
						assert.Equal(t, first.Error(), clients[0].Tag(ext.ErrorMsg))
						assertStatus(t, transport, clients[0], connectrpc.CodeOf(first))
					}
					assert.Len(t, spansNamed(spans, operationMessage), 2, "the Send and the first Receive")
					assert.Len(t, spansNamed(childrenOf(spans, clients[0]), operationMessage), 2)
				})
			}
		}
	})

	// A server-streaming call whose request fails to send ends there: connect.Client returns the
	// error without the stream, which is never closed.
	t.Run("server stream ends when its request fails to send", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			transport testTransport
			options   []connecthttp.Option
			// register registers the server-streaming handler; nil registers the test service.
			register func(*connectrpc.Server)
			request  any
			wantCode connectrpc.Code
		}{
			{
				name: "message too large/connect", transport: connectTransport, request: wrapperspb.String("hello"),
				options: []connecthttp.Option{connecthttp.WithSendMaxBytes(1)}, wantCode: connectrpc.CodeResourceExhausted,
			},
			{
				name: "message too large/grpc", transport: grpcTransport, request: wrapperspb.String("hello"),
				options: []connecthttp.Option{connecthttp.WithSendMaxBytes(1)}, wantCode: connectrpc.CodeResourceExhausted,
			},
			{
				name: "message too large/grpcweb", transport: grpcWebTransport, request: wrapperspb.String("hello"),
				options: []connecthttp.Option{connecthttp.WithSendMaxBytes(1)}, wantCode: connectrpc.CodeResourceExhausted,
			},
			{name: "not a message/connect", transport: connectTransport, request: "hello", wantCode: connectrpc.CodeInternal},
			{name: "not a message/grpc", transport: grpcTransport, request: "hello", wantCode: connectrpc.CodeInternal},
			{
				// connectinprocess reports a server that has returned before receiving the request as
				// a plain io.EOF.
				name: "server returned first/inprocess", transport: inProcessTransport, request: wrapperspb.String("hello"),
				register: func(server *connectrpc.Server) {
					server.Register(connectrpc.Method{
						Spec:    serverStreamSpec,
						Handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error { return nil },
					})
				},
				wantCode: connectrpc.CodeUnknown,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				rig := newTestRig(t, test.transport, rigOptions{register: test.register, transportOptions: test.options})
				stream, err := rig.client(NewClientInterceptor()).CallServerStream(context.Background(), serverStreamSpec, test.request)
				require.Nil(t, stream)
				require.Error(t, err)
				require.Equal(t, test.wantCode, connectrpc.CodeOf(err), "%v", err)

				clients := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, clients, 1, "the call span is finished once the call returns")
				assertStatus(t, test.transport, clients[0], test.wantCode)
				assert.Equal(t, err.Error(), clients[0].Tag(ext.ErrorMsg))
				// The Send carried the call's only request: its error, even an io.EOF, is the call's.
				messages := spansNamed(childrenOf(mt.FinishedSpans(), clients[0]), operationMessage)
				require.Len(t, messages, 1, "the Send")
				assertStatus(t, test.transport, messages[0], test.wantCode)
				assert.Equal(t, err.Error(), messages[0].Tag(ext.ErrorMsg))
			})
		}
	})

	// A bidirectional call read to the end of the stream ends there: Close is optional once Receive
	// has returned io.EOF.
	t.Run("bidi stream ends at the end of the stream without Close", func(t *testing.T) {
		for _, transport := range testTransports {
			t.Run(transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, nil, nil)
				stream, err := client.CallClientStream(context.Background(), bidiSpec)
				require.NoError(t, err)
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				var response wrapperspb.StringValue
				require.NoError(t, stream.Receive(&response))
				assert.Equal(t, "hello", response.Value)
				require.NoError(t, stream.CloseSend())
				assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "the call span finished before the end of the stream")
				require.ErrorIs(t, stream.Receive(&response), io.EOF)

				clients := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, clients, 1)
				assert.Nil(t, clients[0].Tag(ext.ErrorMsg))
				messages := spansNamed(childrenOf(mt.FinishedSpans(), clients[0]), operationMessage)
				assert.Len(t, messages, 3, "a Send, a Receive and the Receive of the end of the stream")
				for _, span := range messages {
					assert.Nil(t, span.Tag(ext.ErrorMsg))
				}
			})
		}
	})

	// connect.Client returns the error of an interceptor that drops the stream it opened, and never
	// closes that stream: the end of the call's context abandons it.
	dropStream := func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			if _, err := next(ctx, spec); err != nil {
				return nil, err
			}
			return nil, connectrpc.NewError(connectrpc.CodeResourceExhausted, "dropped")
		}
	}
	for _, transport := range testTransports {
		for _, rpc := range testRPCs {
			t.Run("finishes when the context of a dropped stream ends/"+rpc.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				// The first interceptor is the outermost.
				client := newTestRig(t, transport, rigOptions{}).client(dropStream, NewClientInterceptor())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				_, err := rpc.call(ctx, client, "hello")
				require.Equal(t, connectrpc.CodeResourceExhausted, connectrpc.CodeOf(err), "%v", err)
				assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "the call span finished before the context ended")
				cancel()
				require.Eventually(t, func() bool {
					return len(spansNamed(mt.FinishedSpans(), operationClient)) == 1
				}, 5*time.Second, time.Millisecond)
				assert.Empty(t, mt.OpenSpans())
				span := spansNamed(mt.FinishedSpans(), operationClient)[0]
				assert.Equal(t, rpc.kind, span.Tag(tagMethodKind))
				assertStatus(t, transport, span, connectrpc.CodeCanceled)
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			})
		}
	}

	// The tracer sends a trace once all of its spans have finished.
	for _, rpc := range testRPCs {
		t.Run("sends the trace of a dropped stream once its context ends/"+rpc.name, func(t *testing.T) {
			tr, agent, err := tracertest.Bootstrap(t, tracer.WithLogger(testutils.DiscardLogger()))
			require.NoError(t, err)
			client := newTestRig(t, inProcessTransport, rigOptions{}).client(dropStream, NewClientInterceptor())
			parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			_, err = rpc.call(ctx, client, "hello")
			require.Equal(t, connectrpc.CodeResourceExhausted, connectrpc.CodeOf(err), "%v", err)
			parent.Finish()
			cancel()
			require.Eventually(t, func() bool {
				tr.Flush()
				return agent.FindSpan(agenttest.With().Operation(operationClient)) != nil
			}, 5*time.Second, 10*time.Millisecond)
			assert.NotNil(t, agent.FindSpan(agenttest.With().Operation("parent")))
		})
	}

	t.Run("SendHeaders starts no span", func(t *testing.T) {
		mt := startMockTracer(t)
		stream, err := traceFakeClient(context.Background(), bidiSpec, &fakeClientStream{}, WithStreamCalls(false))
		require.NoError(t, err)
		require.NoError(t, stream.SendHeaders())
		require.NoError(t, stream.CloseSend())
		assert.Empty(t, mt.FinishedSpans())
	})

	// With the span pool, a finished call span's trace may already be flushed and its spans
	// reused, so a late operation must not start a message span under it.
	t.Run("no message spans after the call span has finished", func(t *testing.T) {
		tr, agent, err := tracertest.Bootstrap(t, tracer.WithSpanPool(true), tracer.WithLogger(testutils.DiscardLogger()))
		require.NoError(t, err)
		client := newTestRig(t, connectTransport, rigOptions{}).client(NewClientInterceptor())
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
			stream, err := client.CallClientStream(context.Background(), bidiSpec)
			require.NoError(t, err)
			require.NoError(t, stream.Send(wrapperspb.String(valueError)))
			err = stream.Receive(&wrapperspb.StringValue{})
			require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)
			_ = stream.Send(wrapperspb.String("late"))
			_ = stream.Receive(&wrapperspb.StringValue{})
			_ = stream.CloseSend()
			require.NoError(t, stream.Close())
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
	for _, transport := range testTransports {
		t.Run("server fails first/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			rig := newTestRig(t, transport, rigOptions{
				serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()},
				register: func(server *connectrpc.Server) {
					server.Register(connectrpc.Method{
						Spec: clientStreamSpec,
						Handler: func(context.Context, connectrpc.Spec, connectrpc.ServerStream) error {
							return connectrpc.NewError(connectrpc.CodeNotFound, "nope")
						},
					})
				},
			})
			stream, err := rig.client(NewClientInterceptor()).CallClientStream(context.Background(), clientStreamSpec)
			require.NoError(t, err)
			// Send fails once the transport has processed the end of the stream.
			var sendErr error
			require.Eventually(t, func() bool {
				sendErr = stream.Send(wrapperspb.String("hello"))
				return sendErr != nil
			}, 5*time.Second, time.Millisecond)
			require.ErrorIs(t, sendErr, io.EOF)
			assert.Empty(t, spansNamed(mt.FinishedSpans(), operationClient), "Send's io.EOF finished the call span")
			require.NoError(t, stream.CloseSend())
			err = stream.Receive(&wrapperspb.StringValue{})
			require.Equal(t, connectrpc.CodeNotFound, connectrpc.CodeOf(err), "%v", err)

			clientSpan, _ := requireCallPair(t, mt, methodKindClientStream)
			assertStatus(t, transport, clientSpan, connectrpc.CodeNotFound)
			assert.Contains(t, clientSpan.Tag(ext.ErrorMsg), "nope")
		})
	}

	// connecthttp wraps the end of the stream with CodeUnknown, and connectinprocess returns it as
	// is, or as the cause of the server's remote verdict when the handler returned it.
	connectEOF := connectrpc.NewError(connectrpc.CodeUnknown, io.EOF.Error()).WithCause(io.EOF)
	remoteEOF := connectrpc.NewError(connectrpc.CodeUnknown, "").WithCause(io.EOF).WithRemote()
	codedEOF := connectrpc.NewError(connectrpc.CodeInternal, "eof").WithCause(io.EOF)
	nope := connectrpc.NewError(connectrpc.CodeNotFound, "nope")
	unavailable := connectrpc.NewError(connectrpc.CodeUnavailable, "unavailable")
	receive := func(s connectrpc.ClientStream) error { return s.Receive(nil) }
	send := func(s connectrpc.ClientStream) error { return s.Send(wrapperspb.String("hello")) }
	for _, test := range []struct {
		name   string
		stream *fakeClientStream
		// ops run in order; their errors must be the stream's own.
		ops       []func(connectrpc.ClientStream) error
		wantErrs  []error
		wantCode  any
		wantError string // a substring of the recorded error; empty if none may be recorded
	}{
		{
			name: "end of stream", stream: &fakeClientStream{},
			ops: []func(connectrpc.ClientStream) error{receive}, wantErrs: []error{io.EOF},
		},
		{
			name: "connect end of stream", stream: &fakeClientStream{receive: returns(connectEOF)},
			ops: []func(connectrpc.ClientStream) error{receive}, wantErrs: []error{connectEOF},
		},
		{
			name: "Send EOF defers to Receive", stream: &fakeClientStream{send: returns(connectEOF), receive: returns(nope)},
			ops: []func(connectrpc.ClientStream) error{send, receive}, wantErrs: []error{connectEOF, nope},
			wantCode: "not_found", wantError: "nope",
		},
		{
			name: "plain Send EOF defers to Receive", stream: &fakeClientStream{send: returns(io.EOF), receive: returns(nope)},
			ops: []func(connectrpc.ClientStream) error{send, receive}, wantErrs: []error{io.EOF, nope},
			wantCode: "not_found", wantError: "nope",
		},
		{
			name: "SendHeaders EOF defers to Receive", stream: &fakeClientStream{sendHeaders: returns(connectEOF), receive: returns(nope)},
			ops:      []func(connectrpc.ClientStream) error{connectrpc.ClientStream.SendHeaders, receive},
			wantErrs: []error{connectEOF, nope}, wantCode: "not_found", wantError: "nope",
		},
		{
			name: "plain SendHeaders EOF defers to Receive", stream: &fakeClientStream{sendHeaders: returns(io.EOF), receive: returns(nope)},
			ops:      []func(connectrpc.ClientStream) error{connectrpc.ClientStream.SendHeaders, receive},
			wantErrs: []error{io.EOF, nope}, wantCode: "not_found", wantError: "nope",
		},
		{
			name: "coded EOF from Send", stream: &fakeClientStream{send: returns(codedEOF)},
			ops: []func(connectrpc.ClientStream) error{send}, wantErrs: []error{codedEOF},
			wantCode: "internal", wantError: "eof",
		},
		{
			name: "coded EOF from Receive", stream: &fakeClientStream{receive: returns(codedEOF)},
			ops: []func(connectrpc.ClientStream) error{receive}, wantErrs: []error{codedEOF},
			wantCode: "internal", wantError: "eof",
		},
		{
			name: "remote EOF from Receive", stream: &fakeClientStream{receive: returns(remoteEOF)},
			ops: []func(connectrpc.ClientStream) error{receive}, wantErrs: []error{remoteEOF},
			wantCode: "unknown", wantError: "unknown",
		},
		{
			name: "SendHeaders error", stream: &fakeClientStream{sendHeaders: returns(unavailable)},
			ops:      []func(connectrpc.ClientStream) error{connectrpc.ClientStream.SendHeaders},
			wantErrs: []error{unavailable}, wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name: "CloseSend error", stream: &fakeClientStream{closeSend: returns(unavailable)},
			ops:      []func(connectrpc.ClientStream) error{connectrpc.ClientStream.CloseSend},
			wantErrs: []error{unavailable}, wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name: "Close", stream: &fakeClientStream{},
			ops: []func(connectrpc.ClientStream) error{connectrpc.ClientStream.Close}, wantErrs: []error{nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			stream, err := traceFakeClient(context.Background(), bidiSpec, test.stream, WithStreamMessages(false))
			require.NoError(t, err)
			errs := make([]error, 0, len(test.ops))
			for _, op := range test.ops {
				errs = append(errs, op(stream))
			}
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

	// The end of the context stands for an abandoned stream. A Receive that is running then and
	// returns the end of the response shows that the caller has the RPC's result.
	for _, test := range []struct {
		name string
		rpc  testRPC
		// deadline ends the context with a deadline while the Receive waits for it; otherwise the
		// context is canceled while the Receive is running.
		deadline bool
		// receive is what the first Receive returns once the context has ended.
		receive   error
		wantCode  connectrpc.Code
		wantError bool
	}{
		{name: "unary response", rpc: unaryRPC},
		{name: "unary response after the deadline", rpc: unaryRPC, deadline: true},
		{name: "client-streaming response", rpc: clientStreamRPC},
		{name: "end of a server stream", rpc: serverStreamRPC, receive: io.EOF},
		{name: "connect end of a server stream", rpc: serverStreamRPC, receive: connectEOF},
		{name: "end of a server stream after the deadline", rpc: serverStreamRPC, deadline: true, receive: io.EOF},
		{name: "end of a bidi stream", rpc: bidiRPC, receive: io.EOF},
		{name: "server stream fails with the cancellation", rpc: serverStreamRPC, receive: context.Canceled, wantCode: connectrpc.CodeCanceled},
		{
			name: "server stream fails with the deadline", rpc: serverStreamRPC, deadline: true,
			receive: context.DeadlineExceeded, wantCode: connectrpc.CodeDeadlineExceeded, wantError: true,
		},
	} {
		t.Run("the end of the response in flight when the context ends/"+test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			ctx, cancel := context.WithCancel(context.Background())
			if test.deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
			}
			defer cancel()
			receiving, release := make(chan struct{}), make(chan struct{})
			stream := &fakeClientStream{receive: func() error {
				close(receiving)
				if test.deadline {
					<-ctx.Done()
				} else {
					<-release
				}
				return test.receive
			}}
			client := connectrpc.NewClient(fakeTransport(fakeClientFunc(stream, connectrpc.ProtocolNameConnect, fakePeerAddr)), NewClientInterceptor())
			called := make(chan error, 1)
			go func() {
				_, err := test.rpc.call(ctx, client, "hello")
				called <- err
			}()
			if !test.deadline {
				<-receiving
				// Returns once the context watcher has started.
				cancel()
				close(release)
			}
			var err error
			requireReturns(t, "the call", func() { err = <-called })
			if test.wantCode == 0 {
				require.True(t, err == nil || errors.Is(err, io.EOF), "%v", err)
			}

			spans := mt.FinishedSpans()
			clients := spansNamed(spans, operationClient)
			require.Len(t, clients, 1)
			assertStatus(t, connectTransport, clients[0], test.wantCode)
			assert.Equal(t, test.wantError, clients[0].Tag(ext.ErrorMsg) != nil, "error: %v", clients[0].Tag(ext.ErrorMsg))
			if test.wantCode == 0 {
				for _, span := range spansNamed(spans, operationMessage) {
					assert.Nil(t, span.Tag(ext.ErrorMsg), "message span")
				}
			}
		})
	}

	// A later call that reuses the CallInfo resets it, and its transport fills it in again.
	t.Run("reads the CallInfo once, when the stream opens", func(t *testing.T) {
		mt := startMockTracer(t)
		ctx, info := connectrpc.NewClientContext(context.Background())
		stream, err := traceFakeClient(ctx, bidiSpec, &fakeClientStream{receive: returns(nil)})
		require.NoError(t, err)
		info.Spec, info.Protocol, info.PeerAddr = unarySpec, connectrpc.ProtocolNameGRPC, "10.0.0.1:443"
		sendHello(stream)
		require.NoError(t, stream.Receive(&wrapperspb.StringValue{}))
		require.NoError(t, stream.CloseSend())
		require.NoError(t, stream.Close())

		spans := mt.FinishedSpans()
		require.Len(t, spansNamed(spans, operationClient), 1)
		require.Len(t, spansNamed(spans, operationMessage), 2)
		for _, span := range spans {
			name := span.OperationName()
			assert.Equal(t, bidiProcedure, span.Tag(ext.ResourceName), name)
			assert.Equal(t, methodKindBidiStream, span.Tag(tagMethodKind), name)
			assert.Equal(t, ext.RPCSystemConnectRPC, span.Tag(ext.RPCSystem), name)
			assert.Nil(t, span.Tag(ext.GRPCFullMethod), name)
			assert.Equal(t, "localhost", span.Tag(ext.NetworkDestinationName), name)
			assert.EqualValues(t, 8080, span.Tag(ext.NetworkDestinationPort), name)
			assert.Nil(t, span.Tag(ext.NetworkDestinationIP), name)
		}
	})
}

// TestStreamingClientTerminalError checks which operations of single-request and single-response
// streams end them, and which error the call span records.
func TestStreamingClientTerminalError(t *testing.T) {
	nope := connectrpc.NewError(connectrpc.CodeNotFound, "nope")
	unavailable := connectrpc.NewError(connectrpc.CodeUnavailable, "unavailable")
	for _, test := range []struct {
		name     string
		spec     connectrpc.Spec
		stream   *fakeClientStream
		run      func(*clientStream)
		wantCode any
		// wantError is a substring of the recorded error; empty if none may be recorded.
		wantError string
	}{
		// The caller of a unary or server-streaming RPC abandons the stream after any failed write.
		{
			name: "server stream: Send EOF ends the stream", spec: serverStreamSpec,
			stream:   &fakeClientStream{send: returns(io.EOF)},
			run:      func(c *clientStream) { sendHello(c) },
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "server stream: SendHeaders EOF ends the stream", spec: serverStreamSpec,
			stream:   &fakeClientStream{sendHeaders: returns(io.EOF)},
			run:      func(c *clientStream) { _ = c.SendHeaders() },
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "server stream: SendHeaders error ends the stream", spec: serverStreamSpec,
			stream:   &fakeClientStream{sendHeaders: returns(unavailable)},
			run:      func(c *clientStream) { _ = c.SendHeaders() },
			wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name: "unary: SendHeaders EOF ends the stream", spec: unarySpec,
			stream:   &fakeClientStream{sendHeaders: returns(io.EOF)},
			run:      func(c *clientStream) { _ = c.SendHeaders() },
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "unary: SendHeaders error ends the stream", spec: unarySpec,
			stream:   &fakeClientStream{sendHeaders: returns(unavailable)},
			run:      func(c *clientStream) { _ = c.SendHeaders() },
			wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name: "server stream: CloseSend EOF ends the stream", spec: serverStreamSpec,
			stream:   &fakeClientStream{closeSend: returns(io.EOF)},
			run:      func(c *clientStream) { _ = c.CloseSend() },
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "server stream: Receive EOF is the end of the stream", spec: serverStreamSpec,
			stream: &fakeClientStream{},
			run: func(c *clientStream) {
				sendHello(c)
				_ = c.CloseSend()
				_ = c.Receive(nil)
			},
		},
		// The first Receive of a unary or client-streaming RPC reads its only response.
		{
			name: "client stream: first Receive ends the stream", spec: clientStreamSpec,
			stream: &fakeClientStream{receive: returns(nil)},
			run:    func(c *clientStream) { _ = c.Receive(nil) },
		},
		{
			name: "client stream: first Receive EOF is an error", spec: clientStreamSpec,
			stream:   &fakeClientStream{},
			run:      func(c *clientStream) { _ = c.Receive(nil) },
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "client stream: Send EOF defers to the first Receive", spec: clientStreamSpec,
			stream: &fakeClientStream{send: returns(io.EOF), receive: returns(nope)},
			run: func(c *clientStream) {
				sendHello(c)
				_ = c.Receive(nil)
			},
			wantCode: "not_found", wantError: "nope",
		},
		{
			name: "client stream: SendHeaders EOF defers to the first Receive", spec: clientStreamSpec,
			stream: &fakeClientStream{sendHeaders: returns(io.EOF), receive: returns(nope)},
			run: func(c *clientStream) {
				_ = c.SendHeaders()
				_ = c.Receive(nil)
			},
			wantCode: "not_found", wantError: "nope",
		},
		{
			name: "client stream: SendHeaders error ends the stream", spec: clientStreamSpec,
			stream:   &fakeClientStream{sendHeaders: returns(unavailable)},
			run:      func(c *clientStream) { _ = c.SendHeaders() },
			wantCode: "unavailable", wantError: "unavailable",
		},
		{
			name: "unary: first Receive EOF is an error", spec: unarySpec,
			stream: &fakeClientStream{},
			run: func(c *clientStream) {
				sendHello(c)
				_ = c.CloseSend()
				_ = c.Receive(nil)
			},
			wantCode: "unknown", wantError: "EOF",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			c := newTestClientStream(newConfig(WithStreamMessages(false)), test.spec, test.stream, tracer.StartSpan(operationClient))
			require.NotPanics(t, func() { test.run(c) })
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
}

func TestStreamingClientErrorCheckReentry(t *testing.T) {
	boom := connectrpc.NewError(connectrpc.CodeInternal, "boom")
	for _, test := range []struct {
		name   string
		spec   connectrpc.Spec
		stream *fakeClientStream
		// expired makes the stream's context done from the start, so the watcher ends it.
		expired bool
		run     func(connectrpc.ClientStream)
	}{
		{
			name: "terminal Receive", spec: bidiSpec, stream: &fakeClientStream{receive: returns(boom)},
			run: func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) },
		},
		{
			name: "first Receive of a client stream", spec: clientStreamSpec, stream: &fakeClientStream{receive: returns(boom)},
			run: func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) },
		},
		{name: "terminal Send", spec: bidiSpec, stream: &fakeClientStream{send: returns(boom)}, run: sendHello},
		{name: "context watcher", spec: bidiSpec, stream: &fakeClientStream{}, expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			var stream connectrpc.ClientStream
			ready, reentered := make(chan struct{}), make(chan struct{})
			var once sync.Once
			errCheck := WithErrorCheck(func(string, error) bool {
				<-ready
				_ = stream.Close()
				once.Do(func() { close(reentered) })
				return true
			})
			ctx := context.Background()
			if test.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
			}
			var err error
			stream, err = traceFakeClient(ctx, test.spec, test.stream, WithStreamMessages(false), errCheck)
			require.NoError(t, err)
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
	connectEOF := connectrpc.NewError(connectrpc.CodeUnknown, io.EOF.Error()).WithCause(io.EOF)
	receive := func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) }
	for _, test := range []struct {
		name string
		spec connectrpc.Spec // bidiSpec if zero
		opts func() []Option
		// openPanic, if set, is called by the transport, which panics with wantPanic instead of
		// opening stream. wantSystem is then the expected rpc.system, and without one the call span
		// has none of the protocol's tags; otherwise it is Connect's.
		openPanic  func(*connectrpc.CallInfo)
		wantSystem any
		stream     *fakeClientStream
		op         func(connectrpc.ClientStream)
		wantPanic  any
		// panicNil runs the case with GODEBUG=panicnil=1, with which recover reports a panic(nil),
		// such as the one wantPanic then stands for, as no panic.
		panicNil  bool
		wantSpans int
	}{
		{name: "opening the stream", openPanic: func(*connectrpc.CallInfo) {}, wantPanic: "open panic", wantSpans: 1},
		{
			name: "opening the stream with an error that panics when unwrapped", openPanic: func(*connectrpc.CallInfo) {},
			wantPanic: wrappedDerefError, wantSpans: 1,
		},
		{
			name: "opening the stream after the transport reported the protocol", wantPanic: "open panic", wantSpans: 1,
			openPanic:  func(info *connectrpc.CallInfo) { info.Protocol = connectrpc.ProtocolNameGRPC },
			wantSystem: ext.RPCSystemGRPC,
		},
		{
			name: "SendHeaders", stream: &fakeClientStream{sendHeaders: panics("send headers panic")},
			op: func(stream connectrpc.ClientStream) { _ = stream.SendHeaders() }, wantPanic: "send headers panic", wantSpans: 1,
		},
		{name: "Send", stream: &fakeClientStream{send: panics("send panic")}, op: sendHello, wantPanic: "send panic", wantSpans: 2},
		{
			name: "Receive", stream: &fakeClientStream{receive: panics("receive panic")},
			op: func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) }, wantPanic: "receive panic", wantSpans: 2,
		},
		{
			name: "Receive with an error that panics when unwrapped", stream: &fakeClientStream{receive: panics(wrappedDerefError)},
			op: func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) }, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Send with an error that panics when unwrapped", stream: &fakeClientStream{send: panics(wrappedDerefError)},
			op: sendHello, wantPanic: wrappedDerefError, wantSpans: 2,
		},
		{
			name: "Receive with io.EOF", opts: func() []Option { return []Option{WithStreamMessages(false)} },
			stream: &fakeClientStream{receive: panics(io.EOF)},
			op:     func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) }, wantPanic: io.EOF, wantSpans: 1,
		},
		// A write that returns connect's end-of-stream signal doesn't end a stream with several
		// requests, but a panic with it does.
		{
			name: "SendHeaders with io.EOF", stream: &fakeClientStream{sendHeaders: panics(io.EOF)},
			op: func(stream connectrpc.ClientStream) { _ = stream.SendHeaders() }, wantPanic: io.EOF, wantSpans: 1,
		},
		{name: "Send with io.EOF", stream: &fakeClientStream{send: panics(io.EOF)}, op: sendHello, wantPanic: io.EOF, wantSpans: 2},
		{
			name: "client-streaming Send with io.EOF", spec: clientStreamSpec, stream: &fakeClientStream{send: panics(io.EOF)},
			op: sendHello, wantPanic: io.EOF, wantSpans: 2,
		},
		{name: "Send with connect's io.EOF", stream: &fakeClientStream{send: panics(connectEOF)}, op: sendHello, wantPanic: connectEOF, wantSpans: 2},
		{
			name: "CloseSend", stream: &fakeClientStream{closeSend: panics("close send panic")},
			op: func(stream connectrpc.ClientStream) { _ = stream.CloseSend() }, wantPanic: "close send panic", wantSpans: 1,
		},
		{
			name: "Close", stream: &fakeClientStream{close: panics("close panic")},
			op: func(stream connectrpc.ClientStream) { _ = stream.Close() }, wantPanic: "close panic", wantSpans: 1,
		},
		{
			name: "unary Send", spec: unarySpec, stream: &fakeClientStream{send: panics("unary send panic")},
			op: sendHello, wantPanic: "unary send panic", wantSpans: 1,
		},
		{
			name: "unary Receive", spec: unarySpec, stream: &fakeClientStream{receive: panics("unary receive panic")},
			op: func(stream connectrpc.ClientStream) { _ = stream.Receive(nil) }, wantPanic: "unary receive panic", wantSpans: 1,
		},
		{
			// The option runs for the call span first, then panics for the message span.
			name: "starting a message span", stream: &fakeClientStream{}, op: sendHello, wantPanic: "span option panic", wantSpans: 1,
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
			opts:   func() []Option { return []Option{WithErrorCheck(func(string, error) bool { panic("errCheck panic") })} },
			stream: &fakeClientStream{send: returns(connectrpc.NewError(connectrpc.CodeInternal, "eof").WithCause(io.EOF))},
		},
		{
			name: "opening the stream with an error whose Error method panics", openPanic: func(*connectrpc.CallInfo) {},
			wantPanic: errorPanics, wantSpans: 1,
		},
		{name: "Receive with an error whose Error method panics", stream: &fakeClientStream{receive: panics(errorPanics)}, op: receive, wantPanic: errorPanics, wantSpans: 2},
		{name: "opening the stream with nil", openPanic: func(*connectrpc.CallInfo) {}, panicNil: true, wantSpans: 1},
		{name: "Receive with nil", stream: &fakeClientStream{receive: panics(nil)}, op: receive, panicNil: true, wantSpans: 2},
		{
			name: "error check with nil", op: sendHello, panicNil: true, wantSpans: 2,
			opts: func() []Option {
				return []Option{WithErrorCheck(func(string, error) bool { panicWith(nil); return true })}
			},
			stream: &fakeClientStream{send: returns(connectrpc.NewError(connectrpc.CodeInternal, "eof").WithCause(io.EOF))},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
			mt := startMockTracer(t)
			var opts []Option
			if test.opts != nil {
				opts = test.opts()
			}
			spec := test.spec
			if spec.Procedure == "" {
				spec = bidiSpec
			}
			open := NewClientInterceptor(opts...)(func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
				if test.openPanic != nil {
					info, _ := connectrpc.CallInfoForClientContext(ctx)
					test.openPanic(info)
					panic(test.wantPanic)
				}
				return fakeClientFunc(test.stream, connectrpc.ProtocolNameConnect, fakePeerAddr)(ctx, spec)
			})
			assert.PanicsWithValue(t, test.wantPanic, func() {
				stream, err := open(context.Background(), spec)
				require.NoError(t, err)
				test.op(stream)
			})
			spans := mt.FinishedSpans()
			require.Len(t, spans, test.wantSpans)
			for _, span := range spans {
				assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
			}
			calls := spansNamed(spans, operationClient)
			require.Len(t, calls, 1)
			wantSystem := any(ext.RPCSystemConnectRPC)
			if test.openPanic != nil {
				wantSystem = test.wantSystem
			}
			assert.Equal(t, wantSystem, calls[0].Tag(ext.RPCSystem))
			if wantSystem == nil {
				assert.Nil(t, calls[0].Tag(tagConnectErrorCode))
				assert.Nil(t, calls[0].Tag(tagGRPCStatusCode))
			}
		})
	}
}

// TestClientUnknownProtocol checks that a call that no transport carried has none of the tags that
// name a protocol, and so no peer.service derived from rpc.service, but records its error. A
// transport without a wire protocol, such as connectinprocess, carries calls as Connect. A call
// that a transport carried keeps the tags of its protocol when an interceptor within the tracing
// one then fails or panics.
func TestClientUnknownProtocol(t *testing.T) {
	failure := connectrpc.NewError(connectrpc.CodeUnavailable, "circuit open")
	// afterOpen closes the stream that the transport opened, then ends the call with end.
	afterOpen := func(end func() (connectrpc.ClientStream, error)) connectrpc.ClientInterceptor {
		return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
			return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
				stream, err := next(ctx, spec)
				if err != nil {
					return nil, err
				}
				_ = stream.Close()
				return end()
			}
		}
	}
	for _, test := range []struct {
		name string
		// open opens the stream in place of the transport, unless it is nil.
		open connectrpc.ClientFunc
		// inner, if set, is an interceptor within the tracing one that fails or panics once the
		// transport has opened the stream: the transport carried the call.
		inner     connectrpc.ClientInterceptor
		wantPanic any
	}{
		{name: "known protocol"},
		{
			name:  "an inner interceptor fails the call that the transport opened",
			inner: afterOpen(func() (connectrpc.ClientStream, error) { return nil, failure }),
		},
		{
			name: "an inner interceptor panics once the transport opened the stream", wantPanic: "inner panic",
			inner: afterOpen(func() (connectrpc.ClientStream, error) { panic("inner panic") }),
		},
		{
			name: "opening panics", wantPanic: "open panic",
			open: func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) { panic("open panic") },
		},
		{
			name: "opening fails",
			open: func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) { return nil, failure },
		},
		{
			name: "a stream that an interceptor returns",
			open: func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) {
				return &fakeClientStream{send: returns(failure), closeSend: returns(failure), receive: returns(failure)}, nil
			},
		},
	} {
		for _, tt := range testTransports {
			t.Run(test.name+"/"+tt.name, func(t *testing.T) {
				tr, agent, err := tracertest.Bootstrap(t, tracer.WithPeerServiceDefaults(true), tracer.WithLogger(testutils.DiscardLogger()))
				require.NoError(t, err)
				interceptors := []connectrpc.ClientInterceptor{NewClientInterceptor()}
				if test.open != nil {
					interceptors = append(interceptors, func(connectrpc.ClientFunc) connectrpc.ClientFunc { return test.open })
				}
				if test.inner != nil {
					interceptors = append(interceptors, test.inner)
				}
				client := newTestRig(t, tt, rigOptions{}).client(interceptors...)
				o := outcomeOf(t, func() { _, _ = callClientStream(context.Background(), client, "hello") })
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
					assert.Equal(t, tt.system, call.Meta[ext.RPCSystem])
					assert.Equal(t, testServiceName, call.Meta[ext.PeerService])
					if test.inner != nil {
						statusTag := tagConnectErrorCode
						if tt.grpc {
							statusTag = tagGRPCStatusCode
						}
						assert.Contains(t, call.Tags, statusTag)
						assert.EqualValues(t, 1, call.Error)
						assert.NotEmpty(t, call.Meta[ext.ErrorMsg])
					}
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
		// stream returns a stream that fails with err, or nil if opening it fails with err.
		stream func(err error) *fakeClientStream
		call   func(connectrpc.ClientStream) error
	}{
		{name: "opening the stream", stream: func(error) *fakeClientStream { return nil }},
		{
			name:   "SendHeaders",
			stream: func(err error) *fakeClientStream { return &fakeClientStream{sendHeaders: returns(err)} },
			call:   connectrpc.ClientStream.SendHeaders,
		},
		{
			name:   "Send",
			stream: func(err error) *fakeClientStream { return &fakeClientStream{send: returns(err)} },
			call:   func(stream connectrpc.ClientStream) error { return stream.Send(wrapperspb.String("hello")) },
		},
		{
			name:   "Receive",
			stream: func(err error) *fakeClientStream { return &fakeClientStream{receive: returns(err)} },
			call:   func(stream connectrpc.ClientStream) error { return stream.Receive(nil) },
		},
		{
			name:   "CloseSend",
			stream: func(err error) *fakeClientStream { return &fakeClientStream{closeSend: returns(err)} },
			call:   connectrpc.ClientStream.CloseSend,
		},
	}
	for _, op := range ops {
		for _, unsafe := range unsafeErrors {
			t.Run(op.name+"/"+unsafe.name, func(t *testing.T) {
				mt := startMockTracer(t)
				fake := op.stream(unsafe.err)
				open := NewClientInterceptor()(func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
					if fake == nil {
						return nil, unsafe.err
					}
					return fakeClientFunc(fake, connectrpc.ProtocolNameConnect, fakePeerAddr)(ctx, spec)
				})
				var stream connectrpc.ClientStream
				var err error
				require.Equal(t, callOutcome{}, outcomeOf(t, func() { stream, err = open(context.Background(), bidiSpec) }))
				if op.call != nil {
					require.NoError(t, err)
					require.Equal(t, callOutcome{}, outcomeOf(t, func() { err = op.call(stream) }))
					requireReturns(t, "Close", func() { _ = stream.Close() })
				} else {
					assert.Nil(t, stream)
				}
				assert.True(t, err == unsafe.err, "got %#v, want the stream's own error", err)
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

	// Telling whether the error of a Receive that Close interrupts is the server's verdict inspects
	// it. Close abandons the call.
	for _, unsafe := range unsafeErrors {
		t.Run("Receive that Close interrupts/"+unsafe.name, func(t *testing.T) {
			mt := startMockTracer(t)
			receiving, closed := make(chan struct{}), make(chan struct{})
			fake := &fakeClientStream{
				receive: func() error {
					close(receiving)
					<-closed
					return unsafe.err
				},
				close: func() error {
					close(closed)
					return nil
				},
			}
			stream, err := traceFakeClient(context.Background(), bidiSpec, fake)
			require.NoError(t, err)
			closeErr := make(chan error, 1)
			go func() {
				<-receiving
				closeErr <- stream.Close()
			}()
			require.Equal(t, callOutcome{}, outcomeOf(t, func() { err = stream.Receive(nil) }))
			requireReturns(t, "Close", func() { assert.NoError(t, <-closeErr) })
			assert.True(t, err == unsafe.err, "got %#v, want the stream's own error", err)
			// The call span and the Receive's message span.
			spans := waitForSpans(t, mt, 2)
			for _, span := range spans {
				assert.Nil(t, span.Tag(ext.ErrorMsg), "%s: %v", span.OperationName(), span.Tags())
			}
			assert.Len(t, spansNamed(spans, operationClient), 1)
		})
	}
}

func TestStreamingClientMetadataTags(t *testing.T) {
	for _, transport := range testTransports {
		t.Run("tags the call spans, not the message spans/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			opts := []Option{WithMetadataTags()}
			_, client := newTracedRig(t, transport, opts, opts)
			ctx, info := connectrpc.NewClientContext(context.Background())
			stream, err := client.CallClientStream(ctx, bidiSpec)
			require.NoError(t, err)
			defer func() { _ = stream.Close() }()
			// Headers set after the stream is opened are still sent, so they are tagged.
			info.RequestHeader().Set("X-Late-Header", "value")
			require.NoError(t, stream.Send(wrapperspb.String("hello")))
			require.NoError(t, stream.Receive(&wrapperspb.StringValue{}))
			require.NoError(t, stream.CloseSend())
			require.ErrorIs(t, stream.Receive(&wrapperspb.StringValue{}), io.EOF)

			clientSpan, serverSpan := requireCallPair(t, mt, methodKindBidiStream)
			assert.Equal(t, "value", metadataTag(clientSpan, "x-late-header"))
			assert.Equal(t, "value", metadataTag(serverSpan, "x-late-header"))
			messages := spansNamed(waitForSpans(t, mt, 8), operationMessage)
			require.Len(t, messages, 6)
			for _, span := range messages {
				assert.Nil(t, metadataTag(span, "x-late-header"))
			}
		})

		t.Run("SendHeaders flushes the headers without a message span/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			_, client := newTracedRig(t, transport, []Option{WithService("server")}, []Option{WithService("client"), WithMetadataTags()})
			ctx, info := connectrpc.NewClientContext(context.Background())
			stream, err := client.CallClientStream(ctx, bidiSpec)
			require.NoError(t, err)
			info.RequestHeader().Set("X-Flush", "value")
			require.NoError(t, stream.SendHeaders())
			require.NoError(t, stream.CloseSend())
			require.ErrorIs(t, stream.Receive(&wrapperspb.StringValue{}), io.EOF)
			require.NoError(t, stream.Close())

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

		t.Run("without call spans, tags one message span per side/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			_, client := newTracedRig(t, transport,
				[]Option{WithService("server"), WithStreamCalls(false), WithMetadataTags()},
				[]Option{WithService("client"), WithStreamCalls(false), WithMetadataTags()})
			ctx, info := connectrpc.NewClientContext(context.Background())
			info.RequestHeader().Set("X-Meta", "value")
			_, err := callBidi(ctx, client, "a", "b")
			require.NoError(t, err)

			// Each side: two Sends and three Receives.
			spans := waitForSpans(t, mt, 10)
			tagged := map[any]int{}
			for _, span := range spans {
				require.Equal(t, operationMessage, span.OperationName())
				if metadataTag(span, "x-meta") != nil {
					tagged[span.Tag(ext.ServiceName)]++
				}
			}
			assert.Equal(t, map[any]int{"client": 1, "server": 1}, tagged)
		})
	}

	// Only the write side reads the metadata, before connect sends it: Receive may run
	// concurrently with the application setting it.
	for _, test := range []struct {
		name string
		spec connectrpc.Spec // bidiSpec if zero
		opts []Option
		// run runs the stream's operations; set sets the metadata.
		run func(stream connectrpc.ClientStream, set func(value string))
		// wantTagged is the finish-order index of the one span with the metadata, or -1 for the
		// call span; wantValue tells which value the write side read.
		wantTagged int
		wantValue  string
	}{
		{
			name: "Receive first, then Send", wantTagged: -1, wantValue: "late",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.Receive(nil)
				set("late")
				sendHello(stream)
				set("later")
				sendHello(stream)
				_ = stream.Close()
			},
		},
		{
			name: "SendHeaders, then Send", opts: []Option{WithStreamMessages(false)}, wantTagged: -1, wantValue: "early",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.SendHeaders()
				set("late")
				sendHello(stream)
				_ = stream.Close()
			},
		},
		{
			name: "unary Send", spec: unarySpec, wantTagged: -1, wantValue: "early",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				sendHello(stream)
				set("late")
				_ = stream.CloseSend()
				_ = stream.Receive(nil)
			},
		},
		{
			name: "without a call span, Receive first, then Send", opts: []Option{WithStreamCalls(false)}, wantTagged: 1, wantValue: "late",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.Receive(nil)
				set("late")
				sendHello(stream)
				set("later")
				sendHello(stream)
			},
		},
		{
			// The value the write side saw is kept, even if the metadata changes afterwards.
			name: "without a call span, SendHeaders, then Receive", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantValue: "early",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.SendHeaders()
				set("late")
				_ = stream.Receive(nil)
				_ = stream.Receive(nil)
			},
		},
		{
			name: "without a call span, Send(nil), then Receive", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantValue: "early",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.Send(nil)
				set("late")
				_ = stream.Receive(nil)
				_ = stream.Receive(nil)
			},
		},
		{
			name: "without a call span, CloseSend, then Receive", opts: []Option{WithStreamCalls(false)}, wantTagged: 0, wantValue: "early",
			run: func(stream connectrpc.ClientStream, set func(string)) {
				_ = stream.CloseSend()
				set("late")
				_ = stream.Receive(nil)
				_ = stream.Receive(nil)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			spec := test.spec
			if spec.Procedure == "" {
				spec = bidiSpec
			}
			ctx, info := connectrpc.NewClientContext(context.Background())
			info.RequestHeader().Set("X-Meta", "early")
			stream, err := traceFakeClient(ctx, spec, &fakeClientStream{receive: returns(nil)}, slices.Concat(test.opts, []Option{WithMetadataTags()})...)
			require.NoError(t, err)
			test.run(stream, func(value string) { info.RequestHeader().Set("X-Meta", value) })
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

	// Under -race, these fail if the interceptor reads the metadata from Receive. connectinprocess
	// itself reads the metadata from the first Receive, so only the HTTP transports allow setting
	// it concurrently.
	for _, mode := range []struct {
		name string
		opts []Option
	}{
		{name: "call span"},
		{name: "message spans", opts: []Option{WithStreamCalls(false)}},
	} {
		for _, transport := range []testTransport{connectTransport, grpcTransport, grpcWebTransport} {
			t.Run("header set after Receive started/"+mode.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				client := newTestRig(t, transport, rigOptions{}).client(NewClientInterceptor(slices.Concat(mode.opts, []Option{WithMetadataTags()})...))
				for range 20 {
					func() {
						ctx, info := connectrpc.NewClientContext(context.Background())
						stream, err := client.CallClientStream(ctx, bidiSpec)
						require.NoError(t, err)
						var wg sync.WaitGroup
						// Close unblocks Receive if the test fails before the end of the stream.
						defer func() {
							_ = stream.Close()
							wg.Wait()
						}()
						started := make(chan struct{})
						wg.Go(func() {
							close(started)
							for {
								if err := stream.Receive(&wrapperspb.StringValue{}); err != nil {
									assert.ErrorIs(t, err, io.EOF)
									return
								}
							}
						})
						<-started
						info.RequestHeader().Set("X-Meta", "value")
						require.NoError(t, stream.Send(wrapperspb.String("hello")))
						require.NoError(t, stream.CloseSend())
						wg.Wait()
					}()
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
		mt := startMockTracer(t)
		ctx, info := connectrpc.NewClientContext(context.Background())
		info.RequestHeader().Set("X-Meta", "value")
		stream, err := traceFakeClient(ctx, bidiSpec, &fakeClientStream{}, WithMetadataTags(), WithStreamCalls(false))
		require.NoError(t, err)
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
		mt := startMockTracer(t)
		ctx, cancel := context.WithCancel(context.Background())
		ctx, info := connectrpc.NewClientContext(ctx)
		info.RequestHeader().Set("X-Late", "value")
		stream, err := traceFakeClient(ctx, bidiSpec, &fakeClientStream{}, WithMetadataTags(), WithStreamMessages(false))
		require.NoError(t, err)
		cancel()
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		assert.Nil(t, metadataTag(mt.FinishedSpans()[0], "x-late"))
	})
}

// TestClientOpenError checks that a call whose stream fails to open finishes its span with the
// error, and only has the tags of a protocol if the transport reported it.
func TestClientOpenError(t *testing.T) {
	// rejectOpened fails the call once the transport has opened its stream.
	rejectOpened := func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			stream, err := next(ctx, spec)
			if err != nil {
				return nil, err
			}
			_ = stream.Close()
			return nil, connectrpc.NewError(connectrpc.CodeUnavailable, "rejected")
		}
	}
	for _, test := range []struct {
		name      string
		transport connectrpc.Transport
		inner     connectrpc.ClientInterceptor
		// system is the expected rpc.system, and grpc whether the gRPC status tags are expected.
		system any
		grpc   bool
		code   connectrpc.Code
	}{
		{name: "bad base URL", transport: connecthttp.NewTransport(http.DefaultClient, "://bad"), code: connectrpc.CodeUnavailable},
		{name: "gRPC bad base URL", transport: connecthttp.NewTransport(http.DefaultClient, "://bad", connecthttp.WithGRPC()), code: connectrpc.CodeUnavailable},
		{
			name:      "unknown codec",
			transport: connecthttp.NewTransport(http.DefaultClient, "http://localhost", connecthttp.WithSendCodec("unknown")),
			code:      connectrpc.CodeUnknown,
		},
		{name: "nil in-process server", transport: connectinprocess.New(nil), code: connectrpc.CodeUnknown},
		{
			name:      "gRPC stream rejected by an inner interceptor",
			transport: fakeTransport(fakeClientFunc(&fakeClientStream{}, connectrpc.ProtocolNameGRPC, fakePeerAddr)),
			inner:     rejectOpened,
			system:    ext.RPCSystemGRPC,
			grpc:      true,
			code:      connectrpc.CodeUnavailable,
		},
		{
			// connectinprocess reports no protocol, but it carried the call.
			name:      "in-process stream rejected by an inner interceptor",
			transport: connectinprocess.New(connectrpc.NewServer()),
			inner:     rejectOpened,
			system:    ext.RPCSystemConnectRPC,
			code:      connectrpc.CodeUnavailable,
		},
	} {
		for _, rpc := range testRPCs {
			t.Run(test.name+"/"+rpc.name, func(t *testing.T) {
				mt := startMockTracer(t)
				interceptors := []connectrpc.ClientInterceptor{NewClientInterceptor()}
				if test.inner != nil {
					interceptors = append(interceptors, test.inner)
				}
				client := connectrpc.NewClient(test.transport, interceptors...)
				_, err := rpc.call(context.Background(), client, "hello")
				require.Error(t, err)
				require.Equal(t, test.code, connectrpc.CodeOf(err), "%v", err)

				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				span := spans[0]
				assert.Equal(t, operationClient, span.OperationName())
				assert.Equal(t, rpc.kind, span.Tag(tagMethodKind))
				assert.Equal(t, rpc.procedure, span.Tag(ext.ResourceName))
				assert.Equal(t, test.system, span.Tag(ext.RPCSystem))
				switch {
				case test.grpc:
					assert.EqualValues(t, test.code, span.Tag(tagGRPCStatusCode))
					assert.Equal(t, rpc.procedure, span.Tag(ext.GRPCFullMethod))
				case test.system != nil:
					assert.Equal(t, test.code.String(), span.Tag(tagConnectErrorCode))
					assert.Nil(t, span.Tag(tagGRPCStatusCode))
					assert.Nil(t, span.Tag(ext.GRPCFullMethod))
				default:
					for _, key := range []string{tagConnectErrorCode, tagGRPCStatusCode, ext.GRPCFullMethod} {
						assert.Nil(t, span.Tag(key), key)
					}
				}
				// The call returns the error that the stream failed to open with.
				assert.Equal(t, err.Error(), span.Tag(ext.ErrorMsg))
				assert.Nil(t, span.Tag(ext.NetworkDestinationIP))
				assert.Nil(t, span.Tag(ext.NetworkDestinationName))
			})
		}
	}

	// The tracer calls Error while it holds the span's lock: the span records a substitute, and the
	// call still returns the original error.
	for _, rpc := range testRPCs {
		t.Run("error whose Error method panics/"+rpc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			openErr := &derefError{}
			failOpen := func(connectrpc.ClientFunc) connectrpc.ClientFunc {
				return func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) { return nil, openErr }
			}
			client := connectrpc.NewClient(connectinprocess.New(connectrpc.NewServer()), NewClientInterceptor(), failOpen)
			var err error
			require.Equal(t, callOutcome{}, outcomeOf(t, func() { _, err = rpc.call(context.Background(), client, "hello") }))
			assert.Same(t, openErr, err)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "panics when formatted")
		})
	}

	// An error can panic only when the tracer formats it, which it does while it holds the span's
	// lock, so the span stays locked. The panic reaches the caller: finishing the span again, as the
	// recovery from a panic of the code that opens the stream does, would deadlock.
	for _, test := range []struct {
		name    string
		openErr func() error
	}{
		{"error whose Error method panics when called again", func() error { return &errorPanicsWhenCalledAgain{} }},
		{"error whose Format method panics", func() error { return formatPanicsError{} }},
	} {
		for _, rpc := range testRPCs {
			t.Run(test.name+"/"+rpc.name, func(t *testing.T) {
				startMockTracer(t)
				failOpen := func(connectrpc.ClientFunc) connectrpc.ClientFunc {
					return func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) { return nil, test.openErr() }
				}
				client := connectrpc.NewClient(connectinprocess.New(connectrpc.NewServer()), NewClientInterceptor(), failOpen)
				o := outcomeOf(t, func() { _, _ = rpc.call(context.Background(), client, "hello") })
				assert.True(t, o.panicked, "%+v", o)
			})
		}
	}

	// A panic in the error check finishes the call span with the panic, and reaches the caller.
	for _, rpc := range testRPCs {
		t.Run("error check panics/"+rpc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			failOpen := func(connectrpc.ClientFunc) connectrpc.ClientFunc {
				return func(context.Context, connectrpc.Spec) (connectrpc.ClientStream, error) {
					return nil, connectrpc.NewError(connectrpc.CodeUnavailable, "rejected")
				}
			}
			errCheck := WithErrorCheck(func(string, error) bool { panic("errCheck panic") })
			client := connectrpc.NewClient(connectinprocess.New(connectrpc.NewServer()), NewClientInterceptor(errCheck), failOpen)
			o := outcomeOf(t, func() { _, _ = rpc.call(context.Background(), client, "hello") })
			assert.Equal(t, callOutcome{panicked: true, value: "errCheck panic"}, o)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "errCheck panic")
		})
	}
}

// errorPanicsWhenCalledAgain returns from its first Error call and panics from the next ones.
type errorPanicsWhenCalledAgain struct{ calls atomic.Int32 }

func (e *errorPanicsWhenCalledAgain) Error() string {
	if e.calls.Add(1) > 1 {
		panic("Error called again")
	}
	return "open failed"
}

// formatPanicsError's Format method panics with a value whose Error method panics too, which fmt
// then re-raises.
type formatPanicsError struct{}

func (formatPanicsError) Error() string          { return "format panics" }
func (formatPanicsError) Format(fmt.State, rune) { panic(&derefError{}) }
