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
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// peerTagKeys are the network.destination.* tags of client spans.
var peerTagKeys = []string{ext.NetworkDestinationIP, ext.NetworkDestinationName, ext.NetworkDestinationPort}

// assertPeer checks the network.destination.* tags of a client span.
func assertPeer(t *testing.T, tt testTransport, span *mocktracer.Span) {
	t.Helper()
	if !tt.hasPeer() {
		for _, key := range peerTagKeys {
			assert.Nil(t, span.Tag(key), key)
		}
		return
	}
	assert.Equal(t, "127.0.0.1", span.Tag(ext.NetworkDestinationIP))
	assert.Nil(t, span.Tag(ext.NetworkDestinationName))
	// The tracer stores the port as a float64 metric.
	port, ok := span.Tag(ext.NetworkDestinationPort).(float64)
	assert.True(t, ok)
	assert.Positive(t, port)
}

// assertCallSpans checks the tags that a client call span and the server call span of the
// same RPC share, their parentage and their peer tags.
func assertCallSpans(t *testing.T, tt testTransport, rpc testRPC, client, server *mocktracer.Span) {
	t.Helper()
	assert.Equal(t, operationClient, client.OperationName())
	assert.Equal(t, operationServer, server.OperationName())
	assert.Equal(t, ext.SpanKindClient, client.Tag(ext.SpanKind))
	assert.Equal(t, ext.SpanKindServer, server.Tag(ext.SpanKind))
	assert.Equal(t, client.TraceID(), server.TraceID())
	assert.Equal(t, client.SpanID(), server.ParentID())
	for _, span := range []*mocktracer.Span{client, server} {
		side := span.Tag(ext.SpanKind)
		assert.Equal(t, rpc.procedure, span.Tag(ext.ResourceName), side)
		assert.Equal(t, testServiceName, span.Tag(ext.RPCService), side)
		assert.Equal(t, rpc.method, span.Tag(ext.RPCMethod), side)
		assert.Equal(t, rpc.kind, span.Tag(tagMethodKind), side)
		assert.Equal(t, tt.system, span.Tag(ext.RPCSystem), side)
		assert.Equal(t, "connectrpc.com/connect.v2", span.Tag(ext.Component), side)
		assert.Equal(t, ext.AppTypeRPC, span.Tag(ext.SpanType), side)
		if tt.grpc {
			assert.Equal(t, rpc.procedure, span.Tag(ext.GRPCFullMethod), side)
		} else {
			assert.Nil(t, span.Tag(ext.GRPCFullMethod), side)
		}
	}
	assertPeer(t, tt, client)
	for _, key := range peerTagKeys {
		assert.Nil(t, server.Tag(key), key)
	}
}

// failedSpans returns the spans that recorded an error.
func failedSpans(spans []*mocktracer.Span) []*mocktracer.Span {
	var failed []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ErrorMsg) != nil {
			failed = append(failed, span)
		}
	}
	return failed
}

func spanByResource(t *testing.T, spans []*mocktracer.Span, resource string) *mocktracer.Span {
	t.Helper()
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == resource {
			return span
		}
	}
	t.Fatalf("no span for %s", resource)
	return nil
}

// sideCallInterceptor returns an interceptor that makes another RPC with call, with the intercepted
// call's context, before each call.
func sideCallInterceptor(call func(context.Context)) connectrpc.ClientInterceptor {
	return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			call(ctx)
			return next(ctx, spec)
		}
	}
}

type originalStreamKey struct{}

func TestUnary(t *testing.T) {
	// WithStreamCalls and WithStreamMessages only apply to streaming RPCs.
	for _, mode := range []struct {
		name string
		opts []Option
	}{
		{name: "default"},
		{name: "without stream calls", opts: []Option{WithStreamCalls(false)}},
		{name: "without stream calls or messages", opts: []Option{WithStreamCalls(false), WithStreamMessages(false)}},
	} {
		for _, transport := range testTransports {
			t.Run(mode.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				opts := append([]Option{
					WithService("connect-test"),
					WithMetadataTags(),
					WithIgnoredMetadata("X-Ignored"),
					WithRequestTags(),
					WithCustomTag("custom-tag", "custom-value"),
					WithSpanOptions(tracer.Tag("span-option", "span-value")),
				}, mode.opts...)
				_, client := newTracedRig(t, transport, opts, opts)
				ctx, info := connectrpc.NewClientContext(context.Background())
				for key, value := range map[string]string{
					"X-Test":            "visible",
					"X-Ignored":         "hidden",
					"Authorization":     "Bearer secret",
					"B3":                "trace-span-1",
					"Ot-Baggage-Secret": "secret",
					"Payload-Bin":       "binary",
					"X-B3-Traceid":      "trace",
				} {
					info.RequestHeader().Set(key, value)
				}
				response, err := callUnary(ctx, client, "hello")
				require.NoError(t, err)
				assert.Equal(t, "hello", response.Value)

				clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
				assert.Len(t, mt.FinishedSpans(), 2)
				assert.Zero(t, clientSpan.ParentID())
				assertCallSpans(t, transport, unaryRPC, clientSpan, serverSpan)
				requestTag, otherRequestTag := tagConnectRequest, tagGRPCRequest
				if transport.grpc {
					requestTag, otherRequestTag = otherRequestTag, requestTag
				}
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					side := span.Tag(ext.SpanKind)
					assert.Equal(t, "connect-test", span.Tag(ext.ServiceName), side)
					assert.Equal(t, "opt.with_service", span.Tag(ext.KeyServiceSource), side)
					assert.Equal(t, "custom-value", span.Tag("custom-tag"), side)
					assert.Equal(t, "span-value", span.Tag("span-option"), side)
					assert.Equal(t, "visible", metadataTag(span, "x-test"), side)
					// x-datadog-trace-id is injected by the client interceptor.
					for _, key := range []string{"authorization", "b3", "ot-baggage-secret", "x-b3-traceid", "x-ignored", "payload-bin", "x-datadog-trace-id"} {
						assert.Nil(t, metadataTag(span, key), "%s %s", side, key)
					}
					assert.Nil(t, span.Tag(ext.ErrorMsg), side)
					assertStatus(t, transport, span, 0)
					assert.Equal(t, `"hello"`, span.Tag(requestTag), side)
					assert.Nil(t, span.Tag(otherRequestTag), side)
				}
			})
		}
	}
}

func TestUnaryErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string // selects the handler's error, see testHandlerError
		opts  []Option
		// code is the status of both spans and the code of the caller's error, and inProcess the one
		// over connectinprocess, if it differs.
		code, inProcess connectrpc.Code
		wantError       bool
		// wantDetails expects the error detail tag, and no stack.
		wantDetails bool
	}{
		{name: "not found is an error", value: valueNotFound, code: connectrpc.CodeNotFound, wantError: true},
		{name: "non-error code keeps the status", value: valueNotFound, opts: []Option{NonErrorCodes(connectrpc.CodeNotFound)}, code: connectrpc.CodeNotFound},
		{name: "explicit code beats the cause", value: valueCodedCanceled, code: connectrpc.CodeInternal, wantError: true},
		{name: "EOF is an error", value: valueEOF, code: connectrpc.CodeUnknown, wantError: true},
		{name: "uncoded error is unknown", value: valuePlainError, code: connectrpc.CodeUnknown, wantError: true},
		{
			name: "error check suppresses the error", value: valueError, code: connectrpc.CodeInternal,
			opts: []Option{WithErrorCheck(func(procedure string, _ error) bool { return procedure != unaryProcedure })},
		},
		{
			name: "error details", value: valueErrorDetails, opts: []Option{WithErrorDetailTags(), NoDebugStack()},
			code: connectrpc.CodeInternal, wantError: true, wantDetails: true,
		},
		// Only connecthttp sends an uncoded error that wraps os.ErrDeadlineExceeded as
		// CodeDeadlineExceeded.
		{name: "uncoded os deadline", value: valueOSDeadline, code: connectrpc.CodeDeadlineExceeded, inProcess: connectrpc.CodeUnknown, wantError: true},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, test.opts, test.opts)
				_, err := callUnary(context.Background(), client, test.value)
				code := test.code
				if transport.inProcess {
					code = cmp.Or(test.inProcess, code)
				}
				require.Equal(t, code, connectrpc.CodeOf(err), "%v", err)

				clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
				assert.Len(t, mt.FinishedSpans(), 2)
				assert.Zero(t, clientSpan.ParentID())
				assertCallSpans(t, transport, unaryRPC, clientSpan, serverSpan)
				detailTag, otherDetailTag := "connect.status_details._0", "grpc.status_details._0"
				if transport.grpc {
					detailTag, otherDetailTag = otherDetailTag, detailTag
				}
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					side := span.Tag(ext.SpanKind)
					assertStatus(t, transport, span, code)
					if test.wantError {
						assert.NotNil(t, span.Tag(ext.ErrorMsg), side)
					} else {
						assert.Nil(t, span.Tag(ext.ErrorMsg), side)
					}
					assert.Nil(t, span.Tag(otherDetailTag), side)
					if test.wantDetails {
						assert.Equal(t, "seconds:1", span.Tag(detailTag), side)
						assert.Nil(t, span.Tag(ext.ErrorHandlingStack), side)
					} else {
						assert.Nil(t, span.Tag(detailTag), side)
					}
				}
			})
		}
	}

	// A single-response call whose handler sends no response is an error on the client: connecthttp
	// reports it as unimplemented, except for Connect unary calls, whose empty body is an empty
	// message, and connectinprocess as an io.EOF on client streams.
	noResponse := func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
		for {
			if err := stream.Receive(&wrapperspb.StringValue{}); err != nil {
				return nil
			}
		}
	}
	for _, rpc := range []testRPC{unaryRPC, clientStreamRPC} {
		for _, transport := range testTransports {
			t.Run("no response/"+rpc.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				rig := newTestRig(t, transport, rigOptions{
					serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()},
					register: func(server *connectrpc.Server) {
						server.Register(
							connectrpc.Method{Spec: unarySpec, Handler: noResponse},
							connectrpc.Method{Spec: clientStreamSpec, Handler: noResponse},
						)
					},
				})
				_, err := rpc.call(context.Background(), rig.client(NewClientInterceptor()), "hello")
				clientSpan, serverSpan := requireCallPair(t, mt, rpc.kind)
				assert.Nil(t, serverSpan.Tag(ext.ErrorMsg))
				if transport.name == connectTransport.name && rpc.kind == methodKindUnary {
					require.NoError(t, err)
					assert.Nil(t, clientSpan.Tag(ext.ErrorMsg))
					return
				}
				require.Error(t, err)
				assert.NotNil(t, clientSpan.Tag(ext.ErrorMsg), clientSpan.Tags())
				if transport.grpc {
					assert.NotZero(t, clientSpan.Tag(tagGRPCStatusCode))
				} else {
					assert.NotNil(t, clientSpan.Tag(tagConnectErrorCode))
				}
				if rpc.kind == methodKindClientStream {
					// The Receive of the missing response.
					assert.Len(t, failedSpans(spansNamed(childrenOf(mt.FinishedSpans(), clientSpan), operationMessage)), 1)
				}
			})
		}
	}

	// A traced server answers as an untraced one, and in time: the tracer calls Error while it holds
	// the span's lock, and with GODEBUG=panicnil=1 recover reports panic(nil) as no panic. A panic
	// fails the call with CodeInternal: connectinprocess recovers it, and HTTP/2 resets the stream.
	for _, test := range []struct {
		name     string
		panicNil bool
		// fail ends the handler after it has received the request.
		fail      func() error
		wantError string // a substring of the error that the traced server's call span records
		// wantCode is the code that the client gets and the status of the server's call span, if set.
		wantCode connectrpc.Code
	}{
		{name: "error whose Error method panics", fail: func() error { return &derefError{} }, wantError: "panics when formatted"},
		{name: "panic(nil)", panicNil: true, fail: func() error { panicWith(nil); return nil }, wantError: "panic: <nil>"},
		{name: "panic", fail: func() error { panic("boom") }, wantError: "panic: boom", wantCode: connectrpc.CodeInternal},
		{
			name: "panic with a coded error", fail: func() error { panic(connectrpc.NewError(connectrpc.CodeNotFound, "boom")) },
			wantError: "not_found: boom", wantCode: connectrpc.CodeInternal,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
			handler := func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
				if err := receiveMessage(ctx, spec, stream); err != nil {
					return err
				}
				return test.fail()
			}
			for _, transport := range testTransports {
				t.Run(transport.name, func(t *testing.T) {
					mt := startMockTracer(t)
					codes := make(map[bool]connectrpc.Code)
					for _, traced := range []bool{false, true} {
						opts := rigOptions{
							register: func(server *connectrpc.Server) {
								server.Register(connectrpc.Method{Spec: unarySpec, Handler: handler})
							},
							discardErrorLog: true,
						}
						if traced {
							opts.serverInterceptors = []connectrpc.ServerInterceptor{NewServerInterceptor()}
						}
						client := newTestRig(t, transport, opts).client()
						var err error
						require.Equal(t, callOutcome{}, outcomeOf(t, func() {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							_, err = callUnary(ctx, client, "hello")
						}), "traced: %v", traced)
						codes[traced] = connectrpc.CodeOf(err)
					}
					assert.Equal(t, codes[false], codes[true])
					assert.NotEqual(t, connectrpc.CodeDeadlineExceeded, codes[true])
					serverSpan := requireServerCallSpan(t, mt)
					assert.Contains(t, serverSpan.Tag(ext.ErrorMsg), test.wantError)
					if test.wantCode != 0 {
						assert.Equal(t, test.wantCode, codes[true])
						assertStatus(t, transport, serverSpan, test.wantCode)
					}
				})
			}
		})
	}
}

func TestStreaming(t *testing.T) {
	modes := []struct {
		name            string
		opts            []Option
		calls, messages bool
	}{
		{name: "calls and messages", calls: true, messages: true},
		{name: "calls only", opts: []Option{WithStreamMessages(false)}, calls: true},
		{name: "messages only", opts: []Option{WithStreamCalls(false)}, messages: true},
	}
	outcomes := []struct {
		name, value string
		// code is the status of the failed spans, or 0 if the call succeeds.
		code connectrpc.Code
	}{
		{name: "ok", value: "hello"},
		{name: "failed", value: valueNotFound, code: connectrpc.CodeNotFound},
		{name: "handler EOF", value: valueEOF, code: connectrpc.CodeUnknown},
	}
	for _, rpc := range testRPCs {
		if rpc.kind == methodKindUnary {
			continue
		}
		for _, transport := range testTransports {
			for _, mode := range modes {
				for _, outcome := range outcomes {
					t.Run(rpc.name+"/"+transport.name+"/"+mode.name+"/"+outcome.name, func(t *testing.T) {
						mt := startMockTracer(t)
						// The service tells the sides apart.
						_, client := newTracedRig(t, transport,
							append([]Option{WithService("server")}, mode.opts...),
							append([]Option{WithService("client")}, mode.opts...))
						values, err := rpc.call(context.Background(), client, outcome.value)
						switch {
						case outcome.code == 0:
							require.NoError(t, err)
							assert.Equal(t, rpc.want, values)
						case outcome.value == valueEOF && transport.inProcess && rpc.kind == methodKindServerStream:
							// connectinprocess keeps the handler's io.EOF as the cause of the client's
							// error, so a caller that reads until io.EOF takes it for the end of the stream.
							require.NoError(t, err)
						default:
							require.Equal(t, outcome.code, connectrpc.CodeOf(err), "%v", err)
						}

						messages := rpc.ok
						if outcome.code != 0 {
							messages = rpc.failed
						}
						want, wantFailed := map[string]int{}, map[string]int{}
						if mode.calls {
							want["connect.client/client"], want["connect.server/server"] = 1, 1
							if outcome.code != 0 {
								wantFailed["connect.client/client"], wantFailed["connect.server/server"] = 1, 1
							}
						}
						if mode.messages {
							want["connect.message/client"], want["connect.message/server"] = messages.client, messages.server
							if outcome.code != 0 {
								// The Receive that got the handler's error.
								wantFailed["connect.message/client"] = 1
							}
						}
						var wantTotal int
						for _, n := range want {
							wantTotal += n
						}
						spans := waitForSpans(t, mt, wantTotal)
						got := map[string]int{}
						callSpanIDs := map[any]uint64{}
						for _, span := range spans {
							got[fmt.Sprint(span.OperationName(), "/", span.Tag(ext.ServiceName))]++
							if span.OperationName() != operationMessage {
								callSpanIDs[span.Tag(ext.ServiceName)] = span.SpanID()
							}
						}
						require.Equal(t, want, got)

						failed := map[string]int{}
						for _, span := range spans {
							side := span.Tag(ext.ServiceName)
							name := fmt.Sprint(span.OperationName(), "/", side)
							assert.Equal(t, rpc.procedure, span.Tag(ext.ResourceName), name)
							assert.Equal(t, testServiceName, span.Tag(ext.RPCService), name)
							assert.Equal(t, rpc.method, span.Tag(ext.RPCMethod), name)
							assert.Equal(t, rpc.kind, span.Tag(tagMethodKind), name)
							assert.Equal(t, transport.system, span.Tag(ext.RPCSystem), name)
							assert.Equal(t, "connectrpc.com/connect.v2", span.Tag(ext.Component), name)
							assert.Equal(t, ext.AppTypeRPC, span.Tag(ext.SpanType), name)
							// The normal end of a stream is not an error.
							if span.Tag(ext.ErrorMsg) != nil {
								failed[name]++
								assertStatus(t, transport, span, outcome.code)
							} else {
								assertStatus(t, transport, span, 0)
							}
							if transport.grpc {
								assert.Equal(t, rpc.procedure, span.Tag(ext.GRPCFullMethod), name)
							} else {
								assert.Nil(t, span.Tag(ext.GRPCFullMethod), name)
							}
							if side == "client" {
								assertPeer(t, transport, span)
							} else {
								assert.Nil(t, span.Tag(ext.NetworkDestinationIP), name)
							}
							switch {
							case span.OperationName() == operationClient:
								assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
								assert.Zero(t, span.ParentID())
							case span.OperationName() == operationServer:
								assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
								assert.Equal(t, callSpanIDs["client"], span.ParentID())
							case mode.calls:
								assert.Nil(t, span.Tag(ext.SpanKind), name)
								assert.Equal(t, callSpanIDs[side], span.ParentID(), name)
								if side == "server" {
									assert.EqualValues(t, 1, span.Tag("_dd.measured"), name)
								} else {
									assert.Nil(t, span.Tag("_dd.measured"), name)
								}
							default:
								// Without call spans, message spans are local roots.
								assert.Equal(t, side, span.Tag(ext.SpanKind), name)
								assert.Zero(t, span.ParentID(), name)
							}
						}
						assert.Equal(t, wantFailed, failed)
					})
				}
			}
		}
	}
}

// TestStreamingFailsAfterAMessage checks streams that fail after a message was exchanged: the
// call spans carry the error, and the earlier message spans don't.
func TestStreamingFailsAfterAMessage(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		// code is the status of the failed spans and the code of the caller's error, and inProcess
		// the one over connectinprocess, if it differs.
		code, inProcess connectrpc.Code
		run             func(t *testing.T, client *connectrpc.Client, code connectrpc.Code)
	}{
		{
			name: "server", kind: methodKindServerStream, code: connectrpc.CodeDataLoss,
			run: func(t *testing.T, client *connectrpc.Client, _ connectrpc.Code) {
				values, err := callServerStream(context.Background(), client, valueFailAfterOne)
				require.Equal(t, connectrpc.CodeDataLoss, connectrpc.CodeOf(err), "%v", err)
				assert.Equal(t, []string{valueFailAfterOne + "-1"}, values)
			},
		},
		{name: "bidi", kind: methodKindBidiStream, code: connectrpc.CodeInternal, run: bidiFailingWith(valueError)},
		// Only connecthttp sends an uncoded error that wraps os.ErrDeadlineExceeded as
		// CodeDeadlineExceeded.
		{
			name: "bidi with an os deadline", kind: methodKindBidiStream, code: connectrpc.CodeDeadlineExceeded, inProcess: connectrpc.CodeUnknown,
			run: bidiFailingWith(valueOSDeadline),
		},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, []Option{WithService("server")}, []Option{WithService("client")})
				code := test.code
				if transport.inProcess {
					code = cmp.Or(test.inProcess, code)
				}
				test.run(t, client, code)
				clientSpan, serverSpan := requireCallPair(t, mt, test.kind)
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
					assertStatus(t, transport, span, code)
				}
				// Only the client's last Receive, which reports the error, fails.
				messages := spansNamed(mt.FinishedSpans(), operationMessage)
				failed := failedSpans(messages)
				require.Len(t, failed, 1, "%d message spans", len(messages))
				assert.Equal(t, "client", failed[0].Tag(ext.ServiceName))
				assertStatus(t, transport, failed[0], code)
			})
		}
	}
}

// bidiFailingWith returns a run function of TestStreamingFailsAfterAMessage that exchanges a
// message on a bidi stream, then sends value, whose error the server fails with.
func bidiFailingWith(value string) func(*testing.T, *connectrpc.Client, connectrpc.Code) {
	return func(t *testing.T, client *connectrpc.Client, code connectrpc.Code) {
		stream, err := client.CallClientStream(context.Background(), bidiSpec)
		require.NoError(t, err)
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		require.NoError(t, stream.Receive(&wrapperspb.StringValue{}))
		require.NoError(t, stream.Send(wrapperspb.String(value)))
		err = stream.Receive(&wrapperspb.StringValue{})
		require.Equal(t, code, connectrpc.CodeOf(err), "%v", err)
		require.NoError(t, stream.CloseSend())
		require.NoError(t, stream.Close())
	}
}

// TestStreamingConcurrentBidi sends and receives on a bidi stream from two goroutines, and closes a
// stream while it receives, as connect allows.
func TestStreamingConcurrentBidi(t *testing.T) {
	const iterations, messages = 10, 20
	for _, transport := range testTransports {
		t.Run(transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			opts := []Option{WithMetadataTags(), WithRequestTags()}
			_, client := newTracedRig(t, transport, append([]Option{WithService("server")}, opts...), append([]Option{WithService("client")}, opts...))
			for range iterations {
				ctx, info := connectrpc.NewClientContext(context.Background())
				if transport.inProcess {
					// connectinprocess copies the request header in the first Send or Receive.
					info.RequestHeader().Set("X-Concurrent", "value")
				}
				stream, err := client.CallClientStream(ctx, bidiSpec)
				require.NoError(t, err)
				var wg sync.WaitGroup
				wg.Go(func() {
					defer func() { assert.NoError(t, stream.Close()) }()
					for {
						err := stream.Receive(&wrapperspb.StringValue{})
						if errors.Is(err, io.EOF) {
							return
						}
						if !assert.NoError(t, err) {
							return
						}
					}
				})
				wg.Go(func() {
					if !transport.inProcess {
						info.RequestHeader().Set("X-Concurrent", "value")
					}
					for range messages {
						if !assert.NoError(t, stream.Send(wrapperspb.String("hello"))) {
							return
						}
					}
					assert.NoError(t, stream.CloseSend())
				})
				wg.Wait()
			}

			// Each side has a call span, a span per message sent, and a span per message received
			// plus the end of the stream.
			want := map[string]int{
				"connect.client/client":  iterations,
				"connect.message/client": iterations * (2*messages + 1),
				"connect.server/server":  iterations,
				"connect.message/server": iterations * (2*messages + 1),
			}
			require.Eventually(t, func() bool {
				return len(spansNamed(mt.FinishedSpans(), operationServer)) == iterations
			}, 5*time.Second, time.Millisecond)
			requestTag := tagConnectRequest
			if transport.grpc {
				requestTag = tagGRPCRequest
			}
			got := map[string]int{}
			metadataTagged, requestTagged := map[any]int{}, map[any]int{}
			for _, span := range mt.FinishedSpans() {
				side := span.Tag(ext.ServiceName)
				got[fmt.Sprint(span.OperationName(), "/", side)]++
				assert.Nil(t, span.Tag(ext.ErrorMsg), "%s: %v", span.OperationName(), span.Tags())
				if metadataTag(span, "x-concurrent") != nil {
					assert.NotEqual(t, operationMessage, span.OperationName())
					metadataTagged[side]++
				}
				if span.Tag(requestTag) != nil {
					requestTagged[side]++
				}
			}
			assert.Equal(t, want, got)
			assert.Equal(t, map[any]int{"client": iterations, "server": iterations}, metadataTagged)
			// The client tags what it sends, the server what it receives.
			assert.Equal(t, map[any]int{"client": iterations * messages, "server": iterations * messages}, requestTagged)
		})

		t.Run("close while receiving/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			_, client := newTracedRig(t, transport, []Option{WithService("server")}, []Option{WithService("client")})
			for range iterations {
				stream, err := client.CallClientStream(context.Background(), bidiSpec)
				require.NoError(t, err)
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				require.NoError(t, stream.Receive(&wrapperspb.StringValue{}))
				received := make(chan error, 1)
				go func() { received <- stream.Receive(&wrapperspb.StringValue{}) }()
				requireReturns(t, "Close", func() { assert.NoError(t, stream.Close()) })
				requireReturns(t, "Receive", func() { assert.Error(t, <-received) })
			}
			require.Eventually(t, func() bool {
				return len(spansNamed(mt.FinishedSpans(), operationServer)) == iterations
			}, 5*time.Second, time.Millisecond)
			clients := spansNamed(mt.FinishedSpans(), operationClient)
			assert.Len(t, clients, iterations)
			for _, span := range clients {
				// Each call span finishes once, after its stream's last operation.
				for _, message := range spansNamed(childrenOf(mt.FinishedSpans(), span), operationMessage) {
					assert.False(t, message.FinishTime().After(span.FinishTime()))
				}
			}
			for _, span := range mt.FinishedSpans() {
				if span.Tag(ext.ServiceName) == "client" {
					assert.Nil(t, span.Tag(ext.ErrorMsg), "%s: %v", span.OperationName(), span.Tags())
				}
			}
		})
	}

	// Close unblocks a pending Receive, which then fails because the stream was torn down:
	// abandoning a stream with Close, like canceling its context, is not an error. A Receive that
	// failed before Close keeps its error, and so does one that was running when Close was called
	// and returns the server's verdict: transports decode it from the response, which Close only
	// discards.
	// torn is what connecthttp returns, for every protocol, to a Receive that Close interrupts: an
	// incomplete envelope, whose code is CodeInternal since connect v2.0.1 (CodeInvalidArgument
	// before it).
	torn := connectrpc.NewError(connectrpc.CodeInternal, "protocol error: incomplete envelope: http2: response body closed")
	verdict := connectrpc.NewError(connectrpc.CodeNotFound, "nope").WithRemote()
	for _, test := range []struct {
		name string
		// interrupted makes Receive return once Close has been called.
		interrupted bool
		// receive is what Receive returns.
		receive   error
		wantError any
	}{
		{name: "close abandons a pending receive", interrupted: true, receive: torn},
		{name: "close after a failed receive", receive: torn, wantError: torn.Error()},
		{name: "close while a receive returns the server's verdict", interrupted: true, receive: verdict, wantError: verdict.Error()},
		{name: "close while a receive returns a message", interrupted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			receiving, closed := make(chan struct{}), make(chan struct{})
			stream, err := traceFakeClient(context.Background(), bidiSpec, &fakeClientStream{
				receive: func() error {
					close(receiving)
					if test.interrupted {
						<-closed
					}
					return test.receive
				},
				close: func() error {
					close(closed)
					return nil
				},
			})
			require.NoError(t, err)
			received := make(chan error, 1)
			go func() { received <- stream.Receive(&wrapperspb.StringValue{}) }()
			<-receiving
			if !test.interrupted {
				require.Equal(t, test.receive, <-received)
			}
			require.NoError(t, stream.Close())
			if test.interrupted {
				requireReturns(t, "Receive", func() { assert.Equal(t, test.receive, <-received) })
			}

			spans := mt.FinishedSpans()
			clients := spansNamed(spans, operationClient)
			require.Len(t, clients, 1)
			assert.Equal(t, test.wantError, clients[0].Tag(ext.ErrorMsg), "call span")
			messages := spansNamed(childrenOf(spans, clients[0]), operationMessage)
			require.Len(t, messages, 1)
			assert.Equal(t, test.wantError, messages[0].Tag(ext.ErrorMsg), "Receive message span")
		})
	}

	// The stream was abandoned before such a Receive started, and an in-process server may answer
	// Close with a verdict.
	t.Run("a receive that starts after close keeps no verdict", func(t *testing.T) {
		mt := startMockTracer(t)
		sending, releaseSend := make(chan struct{}), make(chan struct{})
		stream, err := traceFakeClient(context.Background(), bidiSpec, &fakeClientStream{
			send: func() error {
				close(sending)
				<-releaseSend
				return nil
			},
			receive: returns(verdict),
		})
		require.NoError(t, err)
		// The running Send keeps the call span open.
		sent := make(chan error, 1)
		go func() { sent <- stream.Send(wrapperspb.String("hello")) }()
		<-sending
		require.NoError(t, stream.Close())
		assert.Equal(t, verdict, stream.Receive(&wrapperspb.StringValue{}))
		close(releaseSend)
		requireReturns(t, "Send", func() { assert.NoError(t, <-sent) })

		spans := mt.FinishedSpans()
		clients := spansNamed(spans, operationClient)
		require.Len(t, clients, 1)
		assert.Nil(t, clients[0].Tag(tagConnectErrorCode))
		assert.Nil(t, clients[0].Tag(ext.ErrorMsg))
		messages := spansNamed(childrenOf(spans, clients[0]), operationMessage)
		require.Len(t, messages, 2)
		for _, span := range messages {
			assert.Nil(t, span.Tag(ext.ErrorMsg))
		}
	})

	// connecthttp's Close waits for the response headers, then discards the response body. A
	// Receive that is running then returns either the server's verdict, if it has read it, or the
	// error of the discarded body.
	const raceIterations = 100
	for _, transport := range []testTransport{connectTransport, grpcTransport, grpcWebTransport} {
		t.Run("close races the server's verdict/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			failed := make(chan struct{}, 1)
			rig := newTestRig(t, transport, rigOptions{register: func(server *connectrpc.Server) {
				server.Register(connectrpc.Method{Spec: bidiSpec, Handler: func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
					for {
						var request wrapperspb.StringValue
						if err := stream.Receive(&request); err != nil {
							return err
						}
						if request.Value == valueNotFound {
							failed <- struct{}{}
							return connectrpc.NewError(connectrpc.CodeNotFound, "nope")
						}
						if err := stream.Send(&request); err != nil {
							return err
						}
					}
				}})
			}})
			receiving := make(chan struct{}, 1)
			client := rig.client(NewClientInterceptor(), signalReceives(receiving))
			outcomes := map[string]int{}
			for range raceIterations {
				mt.Reset()
				stream, err := client.CallClientStream(context.Background(), bidiSpec)
				require.NoError(t, err)
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				require.NoError(t, stream.Receive(&wrapperspb.StringValue{}))
				<-receiving
				received := make(chan error, 1)
				go func() { received <- stream.Receive(&wrapperspb.StringValue{}) }()
				<-receiving
				_ = stream.Send(wrapperspb.String(valueNotFound))
				<-failed
				time.Sleep(time.Duration(rand.IntN(200)) * time.Microsecond)
				require.NoError(t, stream.Close())
				var receiveErr error
				requireReturns(t, "Receive", func() { receiveErr = <-received })

				clients := spansNamed(mt.FinishedSpans(), operationClient)
				require.Len(t, clients, 1)
				recorded := clients[0].Tag(ext.ErrorMsg) != nil
				switch gotVerdict := connectrpc.CodeOf(receiveErr) == connectrpc.CodeNotFound; {
				case gotVerdict && recorded:
					outcomes["verdict recorded"]++
					assertStatus(t, transport, clients[0], connectrpc.CodeNotFound)
				case gotVerdict:
					outcomes["verdict missed"]++
				case recorded:
					outcomes["interrupted receive recorded"]++
				default:
					outcomes["interrupted receive"]++
				}
			}
			t.Logf("%d calls: %v", raceIterations, outcomes)
			assert.Zero(t, outcomes["verdict missed"], "%v", outcomes)
			assert.Zero(t, outcomes["interrupted receive recorded"], "%v", outcomes)
		})
	}

	// A Receive that was running when Close was called may also fail with an error that is not the
	// server's verdict, and that Close did not cause either: connecthttp reports a response whose
	// HTTP status rejects the call, as a proxy's or a load balancer's may, as a local error before
	// it reads the body that Close discards. Such an error is not recorded. Telling it from the
	// teardown would mean reading connecthttp's ClientInfo, which races the transport when an
	// interceptor within the tracing one fails the Receive without calling the transport's, or when
	// the context that it gave the transport ends before the response arrives.
	ownError := errors.New("inner interceptor failure")
	for _, test := range []struct {
		name string
		open func(t *testing.T) closeRaceSetup
		// receiveErr checks the error that the Receive returns.
		receiveErr func(t *testing.T, err error)
	}{
		{
			name: "a response whose HTTP status rejects the call",
			open: func(t *testing.T) closeRaceSetup {
				server, respond := newRejectingServer(t)
				return closeRaceSetup{httpClient: server.Client(), baseURL: server.URL, release: func() { close(respond) }}
			},
			receiveErr: func(t *testing.T, err error) {
				assert.Equal(t, connectrpc.CodeUnavailable, connectrpc.CodeOf(err), "%v", err)
			},
		},
		{
			name: "an inner interceptor's own error as such a response arrives",
			open: func(t *testing.T) closeRaceSetup {
				server, respond := newRejectingServer(t)
				proceed := make(chan struct{})
				return closeRaceSetup{
					httpClient: server.Client(),
					baseURL:    server.URL,
					inner: func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
						return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
							stream, err := next(ctx, spec)
							if err != nil {
								return nil, err
							}
							return failingStream{ClientStream: stream, proceed: proceed, respond: respond, err: ownError}, nil
						}
					},
					release: func() { close(proceed) },
				}
			},
			receiveErr: func(t *testing.T, err error) { assert.Equal(t, ownError, err) },
		},
		{
			name:       "the end of the transport's context/" + context.Canceled.Error(),
			open:       endingTransportContext(context.Canceled),
			receiveErr: func(t *testing.T, err error) { assert.ErrorIs(t, err, context.Canceled) },
		},
		{
			name:       "the end of the transport's context/" + context.DeadlineExceeded.Error(),
			open:       endingTransportContext(context.DeadlineExceeded),
			receiveErr: func(t *testing.T, err error) { assert.ErrorIs(t, err, context.DeadlineExceeded) },
		},
	} {
		for _, transport := range []testTransport{connectTransport, grpcTransport, grpcWebTransport} {
			t.Run("close while a receive returns an error that is not the server's verdict/"+test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				setup := test.open(t)
				receiving, closing := make(chan struct{}, 1), make(chan struct{}, 1)
				interceptors := []connectrpc.ClientInterceptor{NewClientInterceptor(), signalOperations(receiving, closing)}
				if setup.inner != nil {
					interceptors = append(interceptors, setup.inner)
				}
				client := connectrpc.NewClient(connecthttp.NewTransport(setup.httpClient, setup.baseURL, transport.httpOptions...), interceptors...)
				stream, err := client.CallClientStream(context.Background(), bidiSpec)
				require.NoError(t, err)
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				received := make(chan error, 1)
				go func() { received <- stream.Receive(&wrapperspb.StringValue{}) }()
				<-receiving
				closed := make(chan error, 1)
				go func() { closed <- stream.Close() }()
				// Close has started, so the Receive returns once Close was called.
				<-closing
				setup.release()
				var receiveErr error
				requireReturns(t, "Receive", func() { receiveErr = <-received })
				requireReturns(t, "Close", func() { <-closed })
				test.receiveErr(t, receiveErr)

				spans := mt.FinishedSpans()
				clients := spansNamed(spans, operationClient)
				require.Len(t, clients, 1)
				assertStatus(t, transport, clients[0], 0)
				for _, span := range spans {
					assert.Nil(t, span.Tag(ext.ErrorMsg), "%s: %v", span.OperationName(), span.Tags())
				}
			})
		}
	}
}

// closeRaceSetup is what a test of a Receive that returns once Close has started runs on.
type closeRaceSetup struct {
	httpClient connecthttp.HTTPClient
	baseURL    string
	// inner, unless nil, runs within the tracing interceptor.
	inner connectrpc.ClientInterceptor
	// release makes the Receive return. It is called once Close has started.
	release func()
}

// newRejectingServer starts a server that answers every request with an HTTP 503, as a proxy may,
// once respond is closed.
func newRejectingServer(t *testing.T) (server *httptest.Server, respond chan struct{}) {
	respond = make(chan struct{})
	server = newHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-respond
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}), false)
	return server, respond
}

// endingTransportContext returns the setup of a call whose transport an inner interceptor gives a
// context that release ends with end, and whose HTTP client answers with an HTTP 503 once that
// context has ended and the transport's Receive has returned.
func endingTransportContext(end error) func(t *testing.T) closeRaceSetup {
	return func(*testing.T) closeRaceSetup {
		httpClient := heldHTTPClient{status: http.StatusServiceUnavailable, release: make(chan struct{})}
		transportContexts := make(chan *endingContext, 1)
		inner := func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
			return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
				ending := newEndingContext(ctx, end)
				transportContexts <- ending
				stream, err := next(ending, spec)
				if err != nil {
					return nil, err
				}
				return releasingStream{ClientStream: stream, release: httpClient.release}, nil
			}
		}
		return closeRaceSetup{
			httpClient: httpClient,
			baseURL:    "http://example.invalid",
			inner:      inner,
			release:    func() { (<-transportContexts).end() },
		}
	}
}

// failingStream's Receive fails with err, without calling the transport's, once proceed is closed.
// It first closes respond, which lets a rejecting server answer, then waits, so that connecthttp
// has recorded the response by then without the Receive synchronizing with it.
type failingStream struct {
	connectrpc.ClientStream
	proceed <-chan struct{}
	respond chan struct{}
	err     error
}

func (s failingStream) Receive(any) error {
	<-s.proceed
	close(s.respond)
	time.Sleep(20 * time.Millisecond)
	return s.err
}

// heldHTTPClient answers a request with status once the request's context has ended and release
// is closed, as net/http may when the response headers arrive as the context ends.
type heldHTTPClient struct {
	status  int
	release chan struct{}
}

func (c heldHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		// connecthttp writes the request messages to a pipe, which Close closes.
		go func() { _, _ = io.Copy(io.Discard, request.Body) }()
	}
	<-request.Context().Done()
	<-c.release
	return &http.Response{
		Status:     fmt.Sprint(c.status, " ", http.StatusText(c.status)),
		StatusCode: c.status,
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    request,
	}, nil
}

// releasingStream closes release once its transport's Receive has returned, which lets a
// heldHTTPClient answer, then waits before returning, so that connecthttp has recorded the
// response by then without the Receive synchronizing with it.
type releasingStream struct {
	connectrpc.ClientStream
	release chan struct{}
}

func (s releasingStream) Receive(message any) error {
	err := s.ClientStream.Receive(message)
	close(s.release)
	time.Sleep(20 * time.Millisecond)
	return err
}

// endingContext is a context that ends with err once end is called, as one that an interceptor
// derives with a cancel function or a deadline does.
type endingContext struct {
	context.Context
	done chan struct{}
	err  error
}

func newEndingContext(parent context.Context, err error) *endingContext {
	return &endingContext{Context: parent, done: make(chan struct{}), err: err}
}

func (c *endingContext) end() { close(c.done) }

func (c *endingContext) Done() <-chan struct{} { return c.done }

func (c *endingContext) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// signalReceives returns an interceptor that sends on receiving whenever a Receive of a stream that
// it opens starts.
func signalReceives(receiving chan<- struct{}) connectrpc.ClientInterceptor {
	return signalOperations(receiving, nil)
}

// signalOperations returns an interceptor that sends on receiving whenever a Receive of a stream
// that it opens starts, and on closing, unless it is nil, whenever a Close starts.
func signalOperations(receiving, closing chan<- struct{}) connectrpc.ClientInterceptor {
	return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			stream, err := next(ctx, spec)
			if err != nil {
				return nil, err
			}
			return signalingStream{ClientStream: stream, receiving: receiving, closing: closing}, nil
		}
	}
}

type signalingStream struct {
	connectrpc.ClientStream
	receiving, closing chan<- struct{}
}

func (s signalingStream) Receive(message any) error {
	s.receiving <- struct{}{}
	return s.ClientStream.Receive(message)
}

func (s signalingStream) Close() error {
	if s.closing != nil {
		s.closing <- struct{}{}
	}
	return s.ClientStream.Close()
}

func TestStreamingWithoutCallSpans(t *testing.T) {
	for _, test := range []struct {
		name string
		// ambient puts the parent in the client's context, and spanOption passes it to
		// WithSpanOptions; with neither, the message spans have no parent.
		ambient, spanOption bool
		// wantClientKind and wantServerKind are the message spans' span.kind, which only local
		// roots have. A parent propagated to the server is remote.
		wantClientKind, wantServerKind any
	}{
		{name: "ambient parent", ambient: true, wantServerKind: "server"},
		{name: "span options parent", spanOption: true},
		{name: "no parent", wantClientKind: "client", wantServerKind: "server"},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				parent := tracer.StartSpan("parent")
				opts := []Option{WithStreamCalls(false), WithRequestTags(), WithMetadataTags()}
				if test.spanOption {
					opts = append(opts, WithSpanOptions(tracer.ChildOf(parent.Context())))
				}
				ctx := context.Background()
				if test.ambient {
					ctx = tracer.ContextWithSpan(ctx, parent)
				}
				wantServerKind := test.wantServerKind
				if test.ambient && transport.inProcess {
					// connectinprocess serves the call on the client's context, so the parent is local.
					wantServerKind = nil
				}
				ctx, info := connectrpc.NewClientContext(ctx)
				info.RequestHeader().Set("X-Message-Header", "value")
				_, client := newTracedRig(t, transport, slices.Concat(opts, []Option{WithService("server")}), slices.Concat(opts, []Option{WithService("client")}))
				echoed, err := callBidi(ctx, client, "hello")
				require.NoError(t, err)
				assert.Equal(t, []string{"hello"}, echoed)
				parent.Finish()

				spans := waitForSpans(t, mt, 7)
				assert.Empty(t, spansNamed(spans, operationClient))
				assert.Empty(t, spansNamed(spans, operationServer))
				messages := spansNamed(spans, operationMessage)
				require.Len(t, messages, 6)
				requestTag := tagConnectRequest
				if transport.grpc {
					requestTag = tagGRPCRequest
				}
				metadataTagged, requestTagged := map[any]int{}, map[any]int{}
				for _, span := range messages {
					side := span.Tag(ext.ServiceName)
					if test.ambient || test.spanOption {
						assert.Equal(t, parent.Context().TraceIDLower(), span.TraceID(), side)
						assert.Equal(t, parent.Context().SpanID(), span.ParentID(), side)
					} else {
						assert.Zero(t, span.ParentID(), side)
					}
					if side == "client" {
						assert.Equal(t, test.wantClientKind, span.Tag(ext.SpanKind))
						assertPeer(t, transport, span)
					} else {
						assert.Equal(t, wantServerKind, span.Tag(ext.SpanKind))
					}
					if value := metadataTag(span, "x-message-header"); value != nil {
						assert.Equal(t, "value", value)
						metadataTagged[side]++
					}
					if value := span.Tag(requestTag); value != nil {
						assert.Equal(t, `"hello"`, value)
						requestTagged[side]++
					}
				}
				// Metadata goes on each side's first message span only. The request is tagged on the
				// client's Send and the server's successful Receive.
				assert.Equal(t, map[any]int{"client": 1, "server": 1}, metadataTagged)
				assert.Equal(t, map[any]int{"client": 1, "server": 1}, requestTagged)
			})
		}
	}
}

// TestStartTimeTags checks the tags that samplers and span options see when a span starts. A
// client only learns its protocol once the transport has opened the stream, after its call span
// has started.
func TestStartTimeTags(t *testing.T) {
	for _, transport := range testTransports {
		t.Run(transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			var (
				mu     sync.Mutex
				starts []map[string]any
			)
			capture := WithSpanOptions(func(cfg *tracer.StartSpanConfig) {
				mu.Lock()
				defer mu.Unlock()
				starts = append(starts, maps.Clone(cfg.Tags))
			})
			_, client := newTracedRig(t, transport, []Option{capture}, []Option{capture})
			_, err := callUnary(context.Background(), client, "hello")
			require.NoError(t, err)
			_, err = callBidi(context.Background(), client, "hello")
			require.NoError(t, err)
			spans := waitForSpans(t, mt, 10)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, starts, 10)
			var clientCalls int
			for _, tags := range starts {
				assert.Contains(t, []any{unaryProcedure, bidiProcedure}, tags[ext.ResourceName])
				assert.Equal(t, testServiceName, tags[ext.RPCService])
				assert.Contains(t, []any{"Unary", "Bidi"}, tags[ext.RPCMethod])
				assert.Contains(t, []any{methodKindUnary, methodKindBidiStream}, tags[tagMethodKind])
				if tags[ext.SpanKind] == ext.SpanKindClient {
					clientCalls++
					assert.NotContains(t, tags, ext.RPCSystem)
					assert.NotContains(t, tags, ext.GRPCFullMethod)
					continue
				}
				assert.Equal(t, transport.system, tags[ext.RPCSystem], tags)
				if transport.grpc {
					assert.Equal(t, tags[ext.ResourceName], tags[ext.GRPCFullMethod])
				} else {
					assert.NotContains(t, tags, ext.GRPCFullMethod)
				}
			}
			assert.Equal(t, 2, clientCalls)
			// The protocol tags are set on client call spans once the stream is open.
			for _, span := range spansNamed(spans, operationClient) {
				assert.Equal(t, transport.system, span.Tag(ext.RPCSystem))
			}
		})
	}
}

// TestPassThrough checks the calls that an interceptor must not trace: calls to an untraced
// procedure, and streams without call and message spans. The client gets the transport's stream
// and the handler the transport's ServerStream, unwrapped. Untraced calls must not inject the
// caller's span, but streams that are only untraced because both of their span types are disabled
// do.
func TestPassThrough(t *testing.T) {
	untraced := func(procedure string) []Option { return []Option{WithUntracedMethods(procedure)} }
	noSpans := []Option{WithStreamCalls(false), WithStreamMessages(false)}
	for _, test := range []struct {
		name string
		opts []Option
		// inner adds a default interceptor after the tested one on each side, as Orchestrion does,
		// which must leave the call untraced too.
		inner        bool
		rpc          testRPC
		wantInjected bool
	}{
		{name: "untraced method/unary", opts: untraced(unaryProcedure), rpc: unaryRPC},
		{name: "untraced method/bidi", opts: untraced(bidiProcedure), rpc: bidiRPC},
		{name: "no call or message spans/bidi", opts: noSpans, rpc: bidiRPC, wantInjected: true},
		{name: "untraced method with an inner interceptor/unary", opts: untraced(unaryProcedure), inner: true, rpc: unaryRPC},
		{name: "untraced method with an inner interceptor/bidi", opts: untraced(bidiProcedure), inner: true, rpc: bidiRPC},
		{name: "no call or message spans with an inner interceptor/bidi", opts: noSpans, inner: true, rpc: bidiRPC, wantInjected: true},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				type received struct {
					traceID  string
					original bool
				}
				calls := make(chan received, 1)
				record := func(handler connectrpc.ServerFunc) connectrpc.ServerFunc {
					return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
						info, _ := connectrpc.CallInfoForServerContext(ctx)
						calls <- received{
							traceID:  info.RequestHeader().Get(tracer.DefaultTraceIDHeader),
							original: stream == ctx.Value(originalStreamKey{}),
						}
						return handler(ctx, spec, stream)
					}
				}
				// keepOriginal passes the transport's stream to the handler.
				keepOriginal := func(next connectrpc.ServerFunc) connectrpc.ServerFunc {
					return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
						return next(context.WithValue(ctx, originalStreamKey{}, stream), spec, stream)
					}
				}
				serverInterceptors := []connectrpc.ServerInterceptor{keepOriginal, NewServerInterceptor(test.opts...)}
				if test.inner {
					serverInterceptors = append(serverInterceptors, NewServerInterceptor())
				}
				rig := newTestRig(t, transport, rigOptions{
					serverInterceptors: serverInterceptors,
					register: func(server *connectrpc.Server) {
						server.Register(
							connectrpc.Method{Spec: unarySpec, Handler: record(handleUnary)},
							connectrpc.Method{Spec: bidiSpec, Handler: record(handleBidi)},
						)
					},
				})
				var opened, returned connectrpc.ClientStream
				keep := func(stream *connectrpc.ClientStream) connectrpc.ClientInterceptor {
					return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
						return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
							s, err := next(ctx, spec)
							*stream = s
							return s, err
						}
					}
				}
				clientInterceptors := []connectrpc.ClientInterceptor{keep(&returned), NewClientInterceptor(test.opts...)}
				if test.inner {
					clientInterceptors = append(clientInterceptors, NewClientInterceptor())
				}
				client := rig.client(append(clientInterceptors, keep(&opened))...)

				parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
				_, err := test.rpc.call(ctx, client, "hello")
				require.NoError(t, err)
				parent.Finish()

				require.NotNil(t, opened)
				assert.True(t, returned == opened, "client stream is wrapped")
				got := <-calls
				assert.True(t, got.original, "handler stream is wrapped")
				assert.Equal(t, test.wantInjected, got.traceID != "", "trace context injected")
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				assert.Equal(t, "parent", spans[0].OperationName())
			})
		}
	}
}

func TestDoubleInstrumentationIsDeduplicated(t *testing.T) {
	const sideProcedure = "/test.connect.v2.TestService/Side"
	sideSpec := connectrpc.Spec{StreamType: connectrpc.StreamTypeUnary, Procedure: sideProcedure}
	registerWithSide := func(server *connectrpc.Server) {
		registerTestService(server)
		server.Register(connectrpc.Method{Spec: sideSpec, Handler: handleUnary})
	}
	clients := func(interceptors ...func() connectrpc.ClientInterceptor) func() []connectrpc.ClientInterceptor {
		return func() []connectrpc.ClientInterceptor {
			built := make([]connectrpc.ClientInterceptor, len(interceptors))
			for i, interceptor := range interceptors {
				built[i] = interceptor()
			}
			return built
		}
	}
	servers := func(interceptors ...func() connectrpc.ServerInterceptor) func() []connectrpc.ServerInterceptor {
		return func() []connectrpc.ServerInterceptor {
			built := make([]connectrpc.ServerInterceptor, len(interceptors))
			for i, interceptor := range interceptors {
				built[i] = interceptor()
			}
			return built
		}
	}
	var (
		client = func() connectrpc.ClientInterceptor { return NewClientInterceptor() }
		server = func() connectrpc.ServerInterceptor { return NewServerInterceptor() }
		// Orchestrion appends its interceptor to a chain that the application instruments manually.
		manualClient = func() connectrpc.ClientInterceptor { return NewClientInterceptor(WithService("manual")) }
		manualServer = func() connectrpc.ServerInterceptor { return NewServerInterceptor(WithService("manual")) }
	)
	for _, test := range []struct {
		name        string
		rpc         testRPC
		client      func() []connectrpc.ClientInterceptor
		server      func() []connectrpc.ServerInterceptor
		wantService any
	}{
		{name: "chained client interceptors/unary", rpc: unaryRPC, client: clients(client, client), server: servers(server)},
		{name: "chained client interceptors/bidi", rpc: bidiRPC, client: clients(client, client), server: servers(server)},
		{name: "chained server interceptors/unary", rpc: unaryRPC, client: clients(client), server: servers(server, server)},
		{name: "chained server interceptors/bidi", rpc: bidiRPC, client: clients(client), server: servers(server, server)},
		{
			name: "manual and woven interceptors/unary", rpc: unaryRPC, wantService: "manual",
			client: clients(manualClient, client), server: servers(manualServer, server),
		},
		{
			name: "manual and woven interceptors/bidi", rpc: bidiRPC, wantService: "manual",
			client: clients(manualClient, client), server: servers(manualServer, server),
		},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				rig := newTestRig(t, transport, rigOptions{serverInterceptors: test.server()})
				_, err := test.rpc.call(context.Background(), rig.client(test.client()...), "hello")
				require.NoError(t, err)
				clientSpan, serverSpan := requireCallPair(t, mt, test.rpc.kind)
				assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
				if test.wantService != nil {
					assert.Equal(t, test.wantService, clientSpan.Tag(ext.ServiceName))
					assert.Equal(t, test.wantService, serverSpan.Tag(ext.ServiceName))
				}
			})
		}
	}

	// An outer interceptor that traces a stream without a call span still handles it.
	for _, transport := range testTransports {
		t.Run("chained interceptors without call spans/bidi/"+transport.name, func(t *testing.T) {
			mt := startMockTracer(t)
			rig := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{
				NewServerInterceptor(WithStreamCalls(false)), NewServerInterceptor(),
			}})
			_, err := callBidi(context.Background(), rig.client(NewClientInterceptor(WithStreamCalls(false)), NewClientInterceptor()), "hello")
			require.NoError(t, err)
			// Each side sends one message and receives it, then the end of the stream.
			spans := waitForSpans(t, mt, 6)
			assert.Len(t, spansNamed(spans, operationMessage), 6)
			assert.Len(t, spans, 6)
		})
	}

	// An RPC that an interceptor makes to another procedure, with the context of the call it
	// intercepts, is another call, which is traced and propagated.
	for _, test := range []struct {
		name string
		rpc  testRPC
		// sideStream makes the side call a bidi stream, rather than a unary call to sideProcedure.
		sideStream   bool
		interceptors func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor
	}{
		{
			name: "inner interceptor makes a unary call/unary", rpc: unaryRPC,
			interceptors: func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor {
				return []connectrpc.ClientInterceptor{NewClientInterceptor(), side}
			},
		},
		{
			name: "inner interceptor makes a unary call/bidi", rpc: bidiRPC,
			interceptors: func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor {
				return []connectrpc.ClientInterceptor{NewClientInterceptor(), side}
			},
		},
		{
			name: "inner interceptor opens a stream/unary", rpc: unaryRPC, sideStream: true,
			interceptors: func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor {
				return []connectrpc.ClientInterceptor{NewClientInterceptor(), side}
			},
		},
		{
			// Orchestrion appends an interceptor to every connect.NewClient call.
			name: "woven chain/unary", rpc: unaryRPC,
			interceptors: func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor {
				return []connectrpc.ClientInterceptor{NewClientInterceptor(), side, NewClientInterceptor()}
			},
		},
		{
			name: "woven chain/bidi", rpc: bidiRPC,
			interceptors: func(side connectrpc.ClientInterceptor) []connectrpc.ClientInterceptor {
				return []connectrpc.ClientInterceptor{NewClientInterceptor(), side, NewClientInterceptor()}
			},
		},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				rig := newTestRig(t, transport, rigOptions{
					serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()},
					register:           registerWithSide,
				})
				sideClient := rig.client(NewClientInterceptor())
				sideResource := sideProcedure
				if test.sideStream {
					sideResource = bidiProcedure
				}
				side := sideCallInterceptor(func(ctx context.Context) {
					if test.sideStream {
						_, err := callBidi(ctx, sideClient, "side")
						assert.NoError(t, err)
						return
					}
					assert.NoError(t, sideClient.CallUnary(ctx, sideSpec, wrapperspb.String("side"), &wrapperspb.StringValue{}))
				})
				_, err := test.rpc.call(context.Background(), rig.client(test.interceptors(side)...), "hello")
				require.NoError(t, err)

				require.Eventually(t, func() bool {
					spans := mt.FinishedSpans()
					return len(spansNamed(spans, operationClient)) >= 2 && len(spansNamed(spans, operationServer)) >= 2
				}, 5*time.Second, time.Millisecond)
				clientSpans, serverSpans := spansNamed(mt.FinishedSpans(), operationClient), spansNamed(mt.FinishedSpans(), operationServer)
				require.Len(t, clientSpans, 2)
				require.Len(t, serverSpans, 2)
				mainClient, sideClientSpan := spanByResource(t, clientSpans, test.rpc.procedure), spanByResource(t, clientSpans, sideResource)
				mainServer, sideServer := spanByResource(t, serverSpans, test.rpc.procedure), spanByResource(t, serverSpans, sideResource)
				assert.Equal(t, mainClient.SpanID(), sideClientSpan.ParentID())
				assert.Equal(t, mainClient.SpanID(), mainServer.ParentID())
				assert.Equal(t, sideClientSpan.SpanID(), sideServer.ParentID())
			})
		}
	}

	// A call made by a handler, with the handler's context, is traced. The transports that serve it
	// on that context parent its server span even without a client interceptor.
	const outerProcedure = "/test.connect.v2.TestService/Outer"
	outerSpec := connectrpc.Spec{StreamType: connectrpc.StreamTypeUnary, Procedure: outerProcedure}
	// forwardUnary returns a handler that forwards its request to unaryProcedure through client.
	forwardUnary := func(client *connectrpc.Client) connectrpc.ServerFunc {
		return func(ctx context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
			var request wrapperspb.StringValue
			if err := stream.Receive(&request); err != nil {
				return err
			}
			response, err := callUnary(ctx, client, request.Value)
			if err != nil {
				return err
			}
			return stream.Send(response)
		}
	}
	for _, transport := range []struct {
		name string
		new  func(*connectrpc.Server) connectrpc.Transport
	}{
		{name: "in-process transport", new: func(server *connectrpc.Server) connectrpc.Transport { return connectinprocess.New(server) }},
		{name: "in-memory transport", new: func(server *connectrpc.Server) connectrpc.Transport {
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			return connecthttp.NewTransport(inMemoryHTTPClient{handler: mux}, "http://in-memory")
		}},
	} {
		for _, test := range []struct {
			name string
			// innerClientTraced adds a client interceptor to the client that the outer handler uses.
			innerClientTraced bool
		}{
			{name: transport.name + " and outbound call from handler", innerClientTraced: true},
			{name: transport.name + " from handler without a client interceptor"},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				innerServer := connectrpc.NewServer(NewServerInterceptor())
				registerTestService(innerServer)
				var innerInterceptors []connectrpc.ClientInterceptor
				if test.innerClientTraced {
					innerInterceptors = append(innerInterceptors, NewClientInterceptor())
				}
				innerClient := connectrpc.NewClient(transport.new(innerServer), innerInterceptors...)
				outerServer := connectrpc.NewServer(NewServerInterceptor())
				outerServer.Register(connectrpc.Method{Spec: outerSpec, Handler: forwardUnary(innerClient)})
				outerClient := connectrpc.NewClient(transport.new(outerServer), NewClientInterceptor())
				var response wrapperspb.StringValue
				require.NoError(t, outerClient.CallUnary(context.Background(), outerSpec, wrapperspb.String("hello"), &response))
				assert.Equal(t, "hello", response.Value)

				clientSpans := spansNamed(mt.FinishedSpans(), operationClient)
				serverSpans := spansNamed(mt.FinishedSpans(), operationServer)
				require.Len(t, serverSpans, 2)
				outerServerSpan, innerServerSpan := spanByResource(t, serverSpans, outerProcedure), spanByResource(t, serverSpans, unaryProcedure)
				assert.Equal(t, spanByResource(t, clientSpans, outerProcedure).SpanID(), outerServerSpan.ParentID())
				if !test.innerClientTraced {
					require.Len(t, clientSpans, 1)
					assert.Equal(t, outerServerSpan.SpanID(), innerServerSpan.ParentID())
					return
				}
				require.Len(t, clientSpans, 2)
				innerClientSpan := spanByResource(t, clientSpans, unaryProcedure)
				assert.Equal(t, outerServerSpan.SpanID(), innerClientSpan.ParentID())
				assert.Equal(t, innerClientSpan.SpanID(), innerServerSpan.ParentID())
			})
		}

		// A handler that forwards the call to the same procedure, as a gateway does, makes another
		// call even when only one side is traced: the outer call's span is then still active in the
		// handler's context, and only the CallInfo tells the calls apart.
		for _, test := range []struct {
			name string
			// clientsTraced traces the clients only; otherwise the servers only.
			clientsTraced bool
		}{
			{name: transport.name + " and same procedure from an untraced handler", clientsTraced: true},
			{name: transport.name + " and same procedure from a handler without a client interceptor"},
		} {
			t.Run(test.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var clientInterceptors []connectrpc.ClientInterceptor
				var serverInterceptors []connectrpc.ServerInterceptor
				tracedOperation := operationServer
				if test.clientsTraced {
					clientInterceptors, tracedOperation = []connectrpc.ClientInterceptor{NewClientInterceptor()}, operationClient
				} else {
					serverInterceptors = []connectrpc.ServerInterceptor{NewServerInterceptor()}
				}
				backend := connectrpc.NewServer(serverInterceptors...)
				registerTestService(backend)
				gateway := connectrpc.NewServer(serverInterceptors...)
				gateway.Register(connectrpc.Method{Spec: unarySpec, Handler: forwardUnary(connectrpc.NewClient(transport.new(backend), clientInterceptors...))})
				response, err := callUnary(context.Background(), connectrpc.NewClient(transport.new(gateway), clientInterceptors...), "hello")
				require.NoError(t, err)
				assert.Equal(t, "hello", response.Value)

				spans := mt.FinishedSpans()
				assert.Len(t, spans, 2)
				traced := spansNamed(spans, tracedOperation)
				require.Len(t, traced, 2)
				// The forwarded call's span finishes first.
				forwarded, outer := traced[0], traced[1]
				assert.Zero(t, outer.ParentID())
				assert.Equal(t, outer.SpanID(), forwarded.ParentID())
				assert.Equal(t, outer.TraceID(), forwarded.TraceID())
				for _, span := range traced {
					assert.Equal(t, unaryProcedure, span.Tag(ext.ResourceName))
				}
			})
		}
	}

	// A call to the same procedure through another client with the context of the traced call, as
	// failover or mirroring interceptors make, shares the traced call's CallInfo: it is part of the
	// traced call, and stays in its trace. Under a span of its own, it is another call.
	for _, rpc := range []testRPC{unaryRPC, bidiRPC} {
		for _, ownSpan := range []bool{false, true} {
			name := "same procedure through another client/"
			if ownSpan {
				name = "same procedure through another client under another span/"
			}
			for _, transport := range testTransports {
				t.Run(name+rpc.name+"/"+transport.name, func(t *testing.T) {
					mt := startMockTracer(t)
					primary := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()}})
					secondary := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor(WithService("secondary"))}})
					secondaryClient := secondary.client(NewClientInterceptor())
					failover := sideCallInterceptor(func(ctx context.Context) {
						if ownSpan {
							var span *tracer.Span
							span, ctx = tracer.StartSpanFromContext(ctx, "failover")
							defer span.Finish()
						}
						_, err := rpc.call(ctx, secondaryClient, "failover")
						assert.NoError(t, err)
					})
					_, err := rpc.call(context.Background(), primary.client(NewClientInterceptor(), failover), "hello")
					require.NoError(t, err)

					require.Eventually(t, func() bool { return len(spansNamed(mt.FinishedSpans(), operationServer)) >= 2 }, 5*time.Second, time.Millisecond)
					var primaryServer, secondaryServer *mocktracer.Span
					for _, span := range spansNamed(mt.FinishedSpans(), operationServer) {
						if span.Tag(ext.ServiceName) == "secondary" {
							secondaryServer = span
						} else {
							primaryServer = span
						}
					}
					require.NotNil(t, primaryServer)
					require.NotNil(t, secondaryServer)
					clientSpans := spansNamed(mt.FinishedSpans(), operationClient)
					if !ownSpan {
						require.Len(t, clientSpans, 1)
						assert.Equal(t, clientSpans[0].SpanID(), secondaryServer.ParentID())
						assert.Equal(t, clientSpans[0].SpanID(), primaryServer.ParentID())
						return
					}
					require.Len(t, clientSpans, 2)
					userSpans := spansNamed(mt.FinishedSpans(), "failover")
					require.Len(t, userSpans, 1)
					mainClient, failoverClient := clientSpans[0], clientSpans[1]
					if mainClient.SpanID() == secondaryServer.ParentID() {
						mainClient, failoverClient = failoverClient, mainClient
					}
					assert.Equal(t, failoverClient.SpanID(), secondaryServer.ParentID())
					assert.Equal(t, userSpans[0].SpanID(), failoverClient.ParentID())
					assert.Equal(t, mainClient.SpanID(), userSpans[0].ParentID())
					assert.Equal(t, mainClient.SpanID(), primaryServer.ParentID())
				})
			}
		}
	}

	// A transport that opens its streams through another traced client shares the CallInfo of the
	// outer client's call, which traces it.
	for _, rpc := range []testRPC{unaryRPC, bidiRPC} {
		for _, transport := range testTransports {
			t.Run("client over a client transport/"+rpc.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				rig := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()}})
				inner := rig.client(NewClientInterceptor())
				outer := connectrpc.NewClient(fakeTransport(func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
					return inner.CallClientStream(ctx, spec)
				}), NewClientInterceptor())
				values, err := rpc.call(context.Background(), outer, "hello")
				require.NoError(t, err)
				assert.Equal(t, rpc.want, values)
				clientSpan, serverSpan := requireCallPair(t, mt, rpc.kind)
				assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
				assert.Equal(t, transport.system, clientSpan.Tag(ext.RPCSystem))
			})
		}
	}
}

// TestHeaderCarrier checks the carrier that injects into and extracts from a connect header.
func TestHeaderCarrier(t *testing.T) {
	cfg := newConfig()
	inject := func(ctx context.Context, header *connectrpc.Header) {
		cfg.core.Inject(ctx, headerCarrier{header: header})
	}

	// A CallInfo reused across calls keeps its request header.
	t.Run("replaces earlier values", func(t *testing.T) {
		startMockTracer(t)
		header := new(connectrpc.Header)
		header.Add(tracer.DefaultParentIDHeader, "1")
		header.Add(tracer.DefaultParentIDHeader, "2")
		first, firstCtx := tracer.StartSpanFromContext(context.Background(), "first")
		defer first.Finish()
		second, secondCtx := tracer.StartSpanFromContext(context.Background(), "second")
		defer second.Finish()
		inject(firstCtx, header)
		inject(secondCtx, header)
		assert.Equal(t, []string{strconv.FormatUint(second.Context().SpanID(), 10)}, header.Values(tracer.DefaultParentIDHeader))
		for key, values := range header.All() {
			assert.Len(t, values, 1, key)
		}
	})

	t.Run("extracts what it injects", func(t *testing.T) {
		mt := startMockTracer(t)
		_, err := tracer.Extract(headerCarrier{header: new(connectrpc.Header)})
		assert.ErrorIs(t, err, tracer.ErrSpanContextNotFound)
		parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		header := new(connectrpc.Header)
		inject(ctx, header)
		spanContext, err := tracer.Extract(headerCarrier{header: header})
		require.NoError(t, err)
		child := tracer.StartSpan("child", tracer.ChildOf(spanContext))
		child.Finish()
		parent.Finish()
		spans := spansNamed(mt.FinishedSpans(), "child")
		require.Len(t, spans, 1)
		assert.Equal(t, parent.Context().SpanID(), spans[0].ParentID())
		assert.Equal(t, parent.Context().TraceIDLower(), spans[0].TraceID())
	})

	t.Run("carrier reads every value", func(t *testing.T) {
		header := new(connectrpc.Header)
		header.Add("X-Multi", "a")
		header.Add("x-multi", "b")
		header.Set("X-Single", "c")
		var got []string
		require.NoError(t, headerCarrier{header: header}.ForeachKey(func(key, value string) error {
			got = append(got, key+"="+value)
			return nil
		}))
		assert.ElementsMatch(t, []string{"X-Multi=a", "X-Multi=b", "X-Single=c"}, got)
	})

	// The carrier marks the values that it injects past their length, which only works because
	// connect keeps the slices that SetValues is given and yields them from All and Values.
	t.Run("connect keeps the slices that it is given", func(t *testing.T) {
		header := new(connectrpc.Header)
		values := make([]string, 1, 2)
		header.SetValues("x-key", values)
		var keys []string
		for key, got := range header.All() {
			keys = append(keys, key)
			assert.Same(t, &values[0], &got[0])
			assert.Equal(t, 2, cap(got))
		}
		assert.Equal(t, []string{"X-Key"}, keys)
		assert.Same(t, &values[0], &header.Values("X-Key")[0])
	})

	t.Run("removes only what it injected", func(t *testing.T) {
		header := new(connectrpc.Header)
		header.Set("Authorization", "Bearer token")
		for _, key := range []string{"Injected", "Set-After", "Added-After"} {
			headerCarrier{header: header}.Set(key, "injected")
		}
		header.Set("Set-After", "application")
		header.Add("Added-After", "application")
		// A slice of the application's with the capacity of a marked one is not marked.
		header.SetValues("Spare-Capacity", append(make([]string, 0, 2), "application"))
		clearInjected(header)
		assert.Equal(t, map[string][]string{
			"Authorization":  {"Bearer token"},
			"Set-After":      {"application"},
			"Added-After":    {"injected", "application"},
			"Spare-Capacity": {"application"},
		}, headerSnapshot(header))
	})

	t.Run("carrier stops at the handler's error", func(t *testing.T) {
		header := new(connectrpc.Header)
		header.Set("X-One", "a")
		header.Set("X-Two", "b")
		stop := errors.New("stop")
		var calls int
		err := headerCarrier{header: header}.ForeachKey(func(string, string) error {
			calls++
			return stop
		})
		assert.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})
}

// TestLibrary checks that connectshared mirrors connect's codes, stream types and protocol names,
// and that library reports what connect's errors are.
func TestLibrary(t *testing.T) {
	t.Run("codes", func(t *testing.T) {
		for code := range connectrpc.Code(21) {
			assert.Equal(t, code.String(), connectshared.Code(code).String(), "code %d", code)
		}
		assert.EqualValues(t, connectrpc.CodeCanceled, connectshared.CodeCanceled)
		assert.EqualValues(t, connectrpc.CodeUnknown, connectshared.CodeUnknown)
		assert.EqualValues(t, connectrpc.CodeDeadlineExceeded, connectshared.CodeDeadlineExceeded)
		assert.EqualValues(t, connectrpc.CodeInternal, connectshared.CodeInternal)
	})

	t.Run("stream types", func(t *testing.T) {
		assert.EqualValues(t, connectrpc.StreamTypeUnary, connectshared.StreamTypeUnary)
		assert.EqualValues(t, connectrpc.StreamTypeClient, connectshared.StreamTypeClient)
		assert.EqualValues(t, connectrpc.StreamTypeServer, connectshared.StreamTypeServer)
		assert.EqualValues(t, connectrpc.StreamTypeBidi, connectshared.StreamTypeBidi)
	})

	t.Run("protocol names", func(t *testing.T) {
		assert.Same(t, connectshared.ProtocolFor("connect"), connectshared.ProtocolFor(connectrpc.ProtocolNameConnect))
		assert.Same(t, connectshared.ProtocolFor("grpc"), connectshared.ProtocolFor(connectrpc.ProtocolNameGRPC))
		assert.Same(t, connectshared.ProtocolFor("grpcweb"), connectshared.ProtocolFor(connectrpc.ProtocolNameGRPCWeb))
	})

	t.Run("inspect error", func(t *testing.T) {
		notFound := connectrpc.NewError(connectrpc.CodeNotFound, "nope")
		remote := notFound.WithRemote()
		for _, test := range []struct {
			name string
			err  error
			want connectshared.ErrorInfo
		}{
			{name: "connect error", err: notFound, want: connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeNotFound)}},
			{name: "wrapped", err: fmt.Errorf("call: %w", notFound), want: connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeNotFound)}},
			{
				name: "explicit code beats a cancellation", err: connectrpc.NewError(connectrpc.CodeInternal, "canceled").WithCause(context.Canceled),
				want: connectshared.ErrorInfo{Coded: true, Code: connectshared.CodeInternal},
			},
			{name: "remote error", err: remote, want: connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeNotFound), Remote: true}},
			{name: "wrapped remote error", err: fmt.Errorf("call: %w", remote), want: connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeNotFound), Remote: true}},
			{
				name: "local error caused by a remote error", err: connectrpc.NewError(connectrpc.CodeUnavailable, "retry").WithCause(remote),
				want: connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeUnavailable)},
			},
			{name: "not modified", err: connecthttp.NewNotModifiedError(), want: connectshared.ErrorInfo{Coded: true, Code: connectshared.CodeUnknown}},
			{name: "typed nil connect error", err: (*connectrpc.Error)(nil), want: connectshared.ErrorInfo{Coded: true, Nil: true, Code: connectshared.CodeUnknown}},
			{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}, want: connectshared.ErrorInfo{Coded: true, Nil: true, Code: connectshared.CodeUnknown}},
			{name: "not a connect error", err: errors.New("boom")},
			{name: "cancellation", err: context.Canceled},
		} {
			t.Run(test.name, func(t *testing.T) {
				var got connectshared.ErrorInfo
				require.NotPanics(t, func() { got = library.InspectError(test.err) })
				assert.Equal(t, test.want, got)
			})
		}

		// Clients receive the server's errors as remote errors.
		for _, tt := range testTransports {
			t.Run("received over "+tt.name, func(t *testing.T) {
				_, err := callUnary(context.Background(), newTestRig(t, tt, rigOptions{}).client(), valueNotFound)
				require.Error(t, err)
				assert.Equal(t, connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectrpc.CodeNotFound), Remote: true}, library.InspectError(err))
			})
		}
	})

	t.Run("not modified", func(t *testing.T) {
		assert.True(t, library.IsNotModified(connecthttp.NewNotModifiedError()))
		assert.True(t, library.IsNotModified(fmt.Errorf("call: %w", connecthttp.NewNotModifiedError())))
		assert.False(t, library.IsNotModified(connectrpc.NewError(connectrpc.CodeUnknown, "not modified")))
		assert.False(t, library.IsNotModified(errors.New("boom")))
	})

	t.Run("error details", func(t *testing.T) {
		detail := errorWithDetails(connectrpc.CodeInternal).Details()[0]
		undecodable := &connectrpc.ErrorDetail{Type: "does.not.Exist", Value: []byte{1}}
		withUndecodable := connectrpc.NewError(connectrpc.CodeInternal, "boom").WithDetail(undecodable).WithDetail(detail)
		for _, test := range []struct {
			name string
			err  error
			want map[int]string
		}{
			{name: "connect", err: errorWithDetails(connectrpc.CodeInternal), want: map[int]string{0: "seconds:1"}},
			{name: "wrapped", err: fmt.Errorf("call: %w", errorWithDetails(connectrpc.CodeInternal)), want: map[int]string{0: "seconds:1"}},
			{name: "remote", err: errorWithDetails(connectrpc.CodeInternal).WithRemote(), want: map[int]string{0: "seconds:1"}},
			{name: "undecodable detail is skipped", err: withUndecodable, want: map[int]string{1: "seconds:1"}},
			{name: "not a connect error", err: errors.New("boom"), want: map[int]string{}},
			{name: "typed nil connect error", err: (*connectrpc.Error)(nil), want: map[int]string{}},
			{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}, want: map[int]string{}},
		} {
			t.Run(test.name, func(t *testing.T) {
				if len(test.want) == 0 {
					// FinishSpan replaces an error that is unsafe to inspect before the library sees
					// it, so the library also gets the error as is. With no detail to tag, the zero
					// ErrorDetailTags is not used.
					require.NotPanics(t, func() { library.ErrorDetails(test.err, connectshared.ErrorDetailTags{}) })
				}
				want := make(map[string]any, len(test.want))
				for index, detail := range test.want {
					want[fmt.Sprintf("connect.status_details._%d", index)] = detail
				}
				finished := finishedWith(t, newConfig(WithErrorDetailTags()), test.err)
				assert.Equal(t, want, tagsWithPrefix(finished, "connect.status_details."))
			})
		}
	})
}

// TestInProcess checks what connectinprocess changes: it serves calls on the client's context,
// has no wire protocol nor network peer, and recovers handler panics.
func TestInProcess(t *testing.T) {
	// The server span's parent comes from the context, even when the client sends no trace headers.
	for _, rpc := range testRPCs {
		t.Run("parent from the context/"+rpc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			rig := newTestRig(t, inProcessTransport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor()}})
			parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
			values, err := rpc.call(ctx, rig.client(), "hello")
			require.NoError(t, err)
			assert.Equal(t, rpc.want, values)
			parent.Finish()

			// The client-streaming client returns once it has received the response, which may be
			// before the handler returns and the server span finishes.
			require.Eventually(t, func() bool { return len(spansNamed(mt.FinishedSpans(), operationServer)) == 1 }, 5*time.Second, time.Millisecond)
			serverSpans := spansNamed(mt.FinishedSpans(), operationServer)
			require.Len(t, serverSpans, 1)
			assert.Equal(t, parent.Context().SpanID(), serverSpans[0].ParentID())
			assert.Equal(t, parent.Context().TraceIDLower(), serverSpans[0].TraceID())
		})
	}

	for _, rpc := range testRPCs {
		t.Run("tags/"+rpc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			_, client := newTracedRig(t, inProcessTransport, nil, nil)
			_, err := rpc.call(context.Background(), client, "hello")
			require.NoError(t, err)
			clientSpan, serverSpan := requireCallPair(t, mt, rpc.kind)
			assertCallSpans(t, inProcessTransport, rpc, clientSpan, serverSpan)
			spans := mt.FinishedSpans()
			assert.Len(t, spans, 2+rpc.ok.client+rpc.ok.server)
			for _, span := range spans {
				assert.Equal(t, ext.RPCSystemConnectRPC, span.Tag(ext.RPCSystem), span.OperationName())
				for _, key := range append([]string{ext.GRPCFullMethod, tagGRPCStatusCode, tagConnectErrorCode}, peerTagKeys...) {
					assert.Nil(t, span.Tag(key), "%s %s", span.OperationName(), key)
				}
			}
		})
	}

	// The transport recovers the handler's panic and fails the call with CodeInternal, the status of
	// the server span, which records the panic that the interceptor re-raises.
	for _, rpc := range testRPCs {
		t.Run("server panic/"+rpc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			_, client := newTracedRig(t, inProcessTransport, nil, nil)
			_, err := rpc.call(context.Background(), client, valuePanic)
			require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)
			assert.ErrorContains(t, err, "panic in server")

			clientSpan, serverSpan := requireCallPair(t, mt, rpc.kind)
			assert.Contains(t, serverSpan.Tag(ext.ErrorMsg), "panic")
			assertStatus(t, inProcessTransport, serverSpan, connectrpc.CodeInternal)
			assert.Contains(t, clientSpan.Tag(ext.ErrorMsg), "panic in server")
			assertStatus(t, inProcessTransport, clientSpan, connectrpc.CodeInternal)
		})
	}

	// connectinprocess keeps the handler's not-modified error, which is not a 304 without HTTP.
	t.Run("not modified", func(t *testing.T) {
		mt := startMockTracer(t)
		_, client := newTracedRig(t, inProcessTransport, nil, nil)
		_, err := callUnary(context.Background(), client, valueNotModified)
		require.True(t, connecthttp.IsNotModifiedError(err), "%v", err)
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			side := span.Tag(ext.SpanKind)
			assert.Nil(t, span.Tag(ext.HTTPCode), side)
			assert.Contains(t, span.Tag(ext.ErrorMsg), "not modified", side)
			assertStatus(t, inProcessTransport, span, connectrpc.CodeUnknown)
		}
	})
}

// TestUnknownHandler checks that a server traces the calls routed to its unknown handler as
// bidi_streaming RPCs of an unrecognized method: the client chose the procedure, so the server's
// spans share one resource and rpc.method, and keep the requested method in rpc.method_original.
func TestUnknownHandler(t *testing.T) {
	const unknownProcedure = "/test.connect.v2.TestService/Unknown"
	manyProcedures := make([]string, 20)
	for i := range manyProcedures {
		manyProcedures[i] = fmt.Sprintf("%s%d", unknownProcedure, i)
	}
	passThrough := func(next connectrpc.ServerFunc) connectrpc.ServerFunc { return next }
	for _, test := range []struct {
		name       string
		streamType connectrpc.StreamType
		// procedures are called in turn; nil calls unknownProcedure.
		procedures []string
		// serverInterceptors precede the traced one, which serverOpts configure, and which wrap, if
		// set, wraps in an interceptor of its own.
		serverInterceptors []connectrpc.ServerInterceptor
		serverOpts         []Option
		wrap               func(connectrpc.ServerInterceptor) connectrpc.ServerInterceptor
		// noUnknownHandler leaves the server without an unknown handler, so that no interceptor runs.
		noUnknownHandler bool
		wantServer       bool
		// registered is set when the procedures are registered methods, whose spans keep their names.
		registered bool
		wantCode   connectrpc.Code
	}{
		{name: "bidi", streamType: connectrpc.StreamTypeBidi, wantServer: true},
		{name: "unary", streamType: connectrpc.StreamTypeUnary, wantServer: true},
		{name: "many procedures", streamType: connectrpc.StreamTypeUnary, procedures: manyProcedures, wantServer: true},
		{
			name: "after another interceptor", streamType: connectrpc.StreamTypeBidi,
			serverInterceptors: []connectrpc.ServerInterceptor{passThrough}, wantServer: true,
		},
		// The stack of SetUnknownHandler is then deeper than a stack trace's usual 32 frames.
		{
			name: "deep within another interceptor", streamType: connectrpc.StreamTypeBidi, wantServer: true,
			wrap: func(ic connectrpc.ServerInterceptor) connectrpc.ServerInterceptor { return nestInterceptor(ic, 40) },
		},
		{
			name: "without call spans", streamType: connectrpc.StreamTypeBidi, procedures: manyProcedures[:3],
			serverOpts: []Option{WithStreamCalls(false)}, wantServer: true,
		},
		// The test service's bidi method is registered without a Schema: its Spec is shaped like the
		// unknown handler's.
		{
			name: "registered method", streamType: connectrpc.StreamTypeBidi, procedures: []string{bidiProcedure},
			wantServer: true, registered: true,
		},
		{name: "untraced", streamType: connectrpc.StreamTypeBidi, serverOpts: []Option{WithUntracedMethods(unknownProcedure)}},
		{name: "no unknown handler", streamType: connectrpc.StreamTypeUnary, noUnknownHandler: true, wantCode: connectrpc.CodeUnimplemented},
	} {
		procedures := test.procedures
		if procedures == nil {
			procedures = []string{unknownProcedure}
		}
		callSpans := newConfig(test.serverOpts...).core.StreamCalls()
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				traced := NewServerInterceptor(test.serverOpts...)
				if test.wrap != nil {
					traced = test.wrap(traced)
				}
				rig := newTestRig(t, transport, rigOptions{
					serverInterceptors: append(slices.Clone(test.serverInterceptors), traced),
					register: func(server *connectrpc.Server) {
						registerTestService(server)
						if !test.noUnknownHandler {
							server.SetUnknownHandler(handleBidi)
						}
					},
				})
				// Every message span is then the server's.
				client := rig.client(NewClientInterceptor(WithStreamMessages(false)))
				for _, procedure := range procedures {
					spec := connectrpc.Spec{StreamType: test.streamType, Procedure: procedure}
					var err error
					if test.streamType == connectrpc.StreamTypeUnary {
						var response wrapperspb.StringValue
						err = client.CallUnary(context.Background(), spec, wrapperspb.String("hello"), &response)
					} else {
						var stream connectrpc.ClientStream
						stream, err = client.CallClientStream(context.Background(), spec)
						require.NoError(t, err)
						require.NoError(t, stream.Send(wrapperspb.String("hello")))
						var response wrapperspb.StringValue
						require.NoError(t, stream.Receive(&response))
						assert.Equal(t, "hello", response.Value)
						require.NoError(t, stream.CloseSend())
						require.ErrorIs(t, stream.Receive(&response), io.EOF)
						require.NoError(t, stream.Close())
					}
					if test.wantCode != 0 {
						require.Equal(t, test.wantCode, connectrpc.CodeOf(err), "%v", err)
					} else {
						require.NoError(t, err)
					}
				}

				if !test.wantServer {
					clientSpans := spansNamed(waitForSpans(t, mt, 1), operationClient)
					require.Len(t, clientSpans, 1)
					if test.wantCode != 0 {
						assertStatus(t, transport, clientSpans[0], test.wantCode)
						assert.NotNil(t, clientSpans[0].Tag(ext.ErrorMsg))
					}
					assert.Empty(t, spansNamed(mt.FinishedSpans(), operationServer))
					assert.Empty(t, spansNamed(mt.FinishedSpans(), operationMessage))
					return
				}
				// The handler receives the request and the end of the stream, and sends the response.
				wantMessages := 3 * len(procedures)
				wantServerSpans := 0
				if callSpans {
					wantServerSpans = len(procedures)
				}
				require.Eventually(t, func() bool {
					spans := mt.FinishedSpans()
					return len(spansNamed(spans, operationServer)) == wantServerSpans && len(spansNamed(spans, operationMessage)) == wantMessages
				}, 5*time.Second, time.Millisecond)
				spans := mt.FinishedSpans()
				// procedureOf maps the client span and the server call span of each call to its procedure.
				procedureOf := make(map[uint64]string)
				clientSpans := spansNamed(spans, operationClient)
				require.Len(t, clientSpans, len(procedures))
				for _, span := range clientSpans {
					procedure := span.Tag(ext.ResourceName).(string)
					procedureOf[span.SpanID()] = procedure
					assert.Equal(t, methodKind(test.streamType), span.Tag(tagMethodKind))
					assert.Equal(t, path.Base(procedure), span.Tag(ext.RPCMethod))
					assert.Nil(t, span.Tag(tagRPCMethodOriginal))
					assert.Nil(t, span.Tag(ext.ErrorMsg))
				}
				assert.ElementsMatch(t, procedures, slices.Collect(maps.Values(procedureOf)))
				serverSpans := spansNamed(spans, operationServer)
				for _, span := range serverSpans {
					procedureOf[span.SpanID()] = procedureOf[span.ParentID()]
				}
				resources := make(map[any]bool)
				for _, span := range append(serverSpans, spansNamed(spans, operationMessage)...) {
					procedure, ok := procedureOf[span.ParentID()]
					require.True(t, ok, "%s span outside of a call", span.OperationName())
					name := span.OperationName() + " " + procedure
					resources[span.Tag(ext.ResourceName)] = true
					assert.Equal(t, testServiceName, span.Tag(ext.RPCService), name)
					assert.Equal(t, methodKindBidiStream, span.Tag(tagMethodKind), name)
					if transport.grpc {
						assert.Equal(t, procedure, span.Tag(ext.GRPCFullMethod), name)
					}
					if test.registered {
						assert.Equal(t, procedure, span.Tag(ext.ResourceName), name)
						assert.Equal(t, path.Base(procedure), span.Tag(ext.RPCMethod), name)
						assert.Nil(t, span.Tag(tagRPCMethodOriginal), name)
						continue
					}
					assert.Equal(t, otherMethod, span.Tag(ext.RPCMethod), name)
					assert.Equal(t, path.Base(procedure), span.Tag(tagRPCMethodOriginal), name)
				}
				if !test.registered {
					assert.Equal(t, map[any]bool{otherMethod: true}, resources, "the server's spans share one resource")
				}
			})
		}
	}
}

// nestInterceptor wraps ic in depth interceptors, each of which wraps the handler with the next one
// as it is itself wrapping, from a deeper call.
func nestInterceptor(ic connectrpc.ServerInterceptor, depth int) connectrpc.ServerInterceptor {
	if depth == 0 {
		return ic
	}
	inner := nestInterceptor(ic, depth-1)
	return func(next connectrpc.ServerFunc) connectrpc.ServerFunc { return wrapWith(inner, next) }
}

//go:noinline
func wrapWith(ic connectrpc.ServerInterceptor, next connectrpc.ServerFunc) connectrpc.ServerFunc {
	return ic(next)
}

// TestRemoteErrorStatus checks that a server span reports a remote error that its handler returns,
// such as the error of a call it made, with the code that the transports send for it,
// CodeInternal, while recording the original error.
func TestRemoteErrorStatus(t *testing.T) {
	for _, rpc := range testRPCs {
		for _, transport := range testTransports {
			t.Run(rpc.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				_, client := newTracedRig(t, transport, nil, nil)
				_, err := rpc.call(context.Background(), client, valueRemoteError)
				require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)
				clientSpan, serverSpan := requireCallPair(t, mt, rpc.kind)
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					assertStatus(t, transport, span, connectrpc.CodeInternal)
					assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
				}
				assert.Contains(t, serverSpan.Tag(ext.ErrorMsg), "upstream not found")
			})
		}
	}

	// A handler returns the error of a call it made to another server.
	for _, test := range []struct {
		name            string
		serverOpts      []Option
		wantServerError bool
	}{
		{name: "nested call", wantServerError: true},
		{name: "NonErrorCodes sees the code sent", serverOpts: []Option{NonErrorCodes(connectrpc.CodeInternal)}},
		{name: "NonErrorCodes ignores the remote code", serverOpts: []Option{NonErrorCodes(connectrpc.CodeNotFound)}, wantServerError: true},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				upstream := connectrpc.NewServer()
				registerTestService(upstream)
				upstreamClient := connectrpc.NewClient(connectinprocess.New(upstream))
				rig := newTestRig(t, transport, rigOptions{
					serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor(test.serverOpts...)},
					register: func(server *connectrpc.Server) {
						server.Register(connectrpc.Method{Spec: unarySpec, Handler: func(ctx context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
							var request wrapperspb.StringValue
							if err := stream.Receive(&request); err != nil {
								return err
							}
							response, err := callUnary(ctx, upstreamClient, request.Value)
							if err != nil {
								return err
							}
							return stream.Send(response)
						}})
					},
				})
				_, err := callUnary(context.Background(), rig.client(NewClientInterceptor()), valueNotFound)
				require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err), "%v", err)
				clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					assertStatus(t, transport, span, connectrpc.CodeInternal)
				}
				assert.NotNil(t, clientSpan.Tag(ext.ErrorMsg))
				if test.wantServerError {
					assert.Contains(t, serverSpan.Tag(ext.ErrorMsg), "not found")
				} else {
					assert.Nil(t, serverSpan.Tag(ext.ErrorMsg))
				}
			})
		}
	}
}

// TestReusedCallInfo checks calls that share a CallInfo, attached with connect.NewClientContext:
// each call sends the application's headers and only its own trace headers.
func TestReusedCallInfo(t *testing.T) {
	propagationKeys := []string{tracer.DefaultTraceIDHeader, tracer.DefaultParentIDHeader, tracer.DefaultPriorityHeader, "traceparent", "tracestate"}
	for _, rpc := range testRPCs {
		for _, transport := range testTransports {
			t.Run(rpc.name+"/"+transport.name, func(t *testing.T) {
				mt := startMockTracer(t)
				var (
					mu       sync.Mutex
					received []*connectrpc.Header
				)
				record := func(next connectrpc.ServerFunc) connectrpc.ServerFunc {
					return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
						info, _ := connectrpc.CallInfoForServerContext(ctx)
						mu.Lock()
						received = append(received, cloneHeader(info.RequestHeader()))
						mu.Unlock()
						return next(ctx, spec, stream)
					}
				}
				rig := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{record, NewServerInterceptor()}})
				client := rig.client(NewClientInterceptor())
				ctx, info := connectrpc.NewClientContext(context.Background())
				for range 2 {
					values, err := rpc.call(ctx, client, "hello")
					require.NoError(t, err)
					assert.Equal(t, rpc.want, values)
				}

				require.Eventually(t, func() bool { return len(spansNamed(mt.FinishedSpans(), operationServer)) == 2 }, 5*time.Second, time.Millisecond)
				clientSpans, serverSpans := spansNamed(mt.FinishedSpans(), operationClient), spansNamed(mt.FinishedSpans(), operationServer)
				require.Len(t, clientSpans, 2)
				assert.NotEqual(t, clientSpans[0].TraceID(), clientSpans[1].TraceID())
				mu.Lock()
				defer mu.Unlock()
				require.Len(t, received, 2)
				for i, clientSpan := range clientSpans {
					assert.Zero(t, clientSpan.ParentID())
					children := childrenOf(serverSpans, clientSpan)
					require.Len(t, children, 1, "call %d", i)
					for _, key := range propagationKeys {
						assert.LessOrEqual(t, len(received[i].Values(key)), 1, "call %d %s", i, key)
					}
					assert.Equal(t, strconv.FormatUint(clientSpan.SpanID(), 10), received[i].Get(tracer.DefaultParentIDHeader), "call %d", i)
				}
				assert.Equal(t, []string{strconv.FormatUint(clientSpans[1].SpanID(), 10)}, info.RequestHeader().Values(tracer.DefaultParentIDHeader))
			})
		}
	}

	// serverCallView is what a server call span sees of a call.
	type serverCallView struct {
		header   *connectrpc.Header
		traceID  string
		parentID uint64
		origin   string
	}
	// The trace headers of the scenarios below, as connect canonicalizes them. The My-* headers are
	// those of the custom propagator.
	traceHeaders := []string{
		"X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "X-Datadog-Sampling-Priority", "X-Datadog-Origin", "X-Datadog-Tags",
		"Traceparent", "Tracestate", "Baggage", "Ot-Baggage-User",
		"My-Trace-Id", "My-Parent-Id", "My-Priority", "My-Baggage-User",
	}
	// A synthetics test with a 128-bit trace ID and baggage, and an unrelated 64-bit trace.
	synthetics := tracer.TextMapCarrier{
		tracer.DefaultTraceIDHeader: "1111", tracer.DefaultParentIDHeader: "2222", tracer.DefaultPriorityHeader: "1",
		"x-datadog-origin": "synthetics", "x-datadog-tags": "_dd.p.tid=640cfd8d00000000", "baggage": "user=alice",
	}
	upstream := tracer.TextMapCarrier{tracer.DefaultTraceIDHeader: "3333", tracer.DefaultParentIDHeader: "4444", tracer.DefaultPriorityHeader: "1"}
	untraced := []Option{WithUntracedMethods(unaryProcedure)}
	setBaggage := func(header *connectrpc.Header) { header.Set("Baggage", "tenant=acme") }
	type reusedCall struct {
		client []Option
		rpc    testRPC
		// parent is the trace context that the caller continues; nil runs the call without an
		// active span.
		parent tracer.TextMapCarrier
		// before changes the request header before the call, as the application would.
		before func(*connectrpc.Header)
		// sent are the values of headers that the server receives; an empty value is not sent.
		sent map[string]string
		// propagated is set if the server span continues the caller's trace. Otherwise the server
		// receives none of traceHeaders but those in sent, and its span is a root.
		propagated bool
	}
	for _, test := range []struct {
		name string
		// propagator, if set, configures the tracer that replaces the mock tracer.
		propagator *tracer.PropagatorConfig
		calls      []reusedCall
	}{
		{
			name: "a traced call sends only its own trace headers",
			calls: []reusedCall{
				{rpc: unaryRPC, parent: synthetics, propagated: true, sent: map[string]string{"X-Datadog-Origin": "synthetics", "Baggage": "user=alice", "Ot-Baggage-User": "alice"}},
				{rpc: unaryRPC, parent: upstream, propagated: true, sent: map[string]string{"X-Datadog-Origin": "", "Baggage": "", "Ot-Baggage-User": ""}},
			},
		},
		{
			name: "an untraced call sends no trace headers",
			calls: []reusedCall{
				{rpc: unaryRPC, parent: synthetics, propagated: true},
				{client: untraced, rpc: unaryRPC},
			},
		},
		{
			name: "a stream without a span sends no trace headers",
			calls: []reusedCall{
				{rpc: unaryRPC, parent: synthetics, propagated: true},
				{client: []Option{WithStreamCalls(false)}, rpc: serverStreamRPC},
			},
		},
		{
			name: "the application's headers are kept",
			calls: []reusedCall{
				{rpc: unaryRPC, parent: synthetics, propagated: true, sent: map[string]string{"Baggage": "user=alice"}},
				{rpc: unaryRPC, parent: upstream, before: setBaggage, propagated: true, sent: map[string]string{"Baggage": "tenant=acme", "Ot-Baggage-User": ""}},
				{client: untraced, rpc: unaryRPC, sent: map[string]string{"Baggage": "tenant=acme"}},
			},
		},
		{
			name:  "a fresh CallInfo sends the application's baggage",
			calls: []reusedCall{{rpc: unaryRPC, parent: upstream, before: setBaggage, propagated: true, sent: map[string]string{"Baggage": "tenant=acme"}}},
		},
		{
			name: "the headers of a custom propagator",
			propagator: &tracer.PropagatorConfig{
				TraceHeader: "my-trace-id", ParentHeader: "my-parent-id", PriorityHeader: "my-priority", BaggagePrefix: "my-baggage-",
			},
			calls: []reusedCall{
				{
					rpc: unaryRPC, parent: tracer.TextMapCarrier{"my-trace-id": "1111", "my-parent-id": "2222", "my-priority": "1", "my-baggage-user": "alice"},
					propagated: true, sent: map[string]string{"My-Baggage-User": "alice", "Baggage": "user=alice"},
				},
				{client: untraced, rpc: unaryRPC},
			},
		},
	} {
		for _, transport := range testTransports {
			t.Run(test.name+"/"+transport.name, func(t *testing.T) {
				if test.propagator == nil {
					startMockTracer(t)
				} else {
					_, _, err := tracertest.Bootstrap(t, tracer.WithPropagator(tracer.NewPropagator(test.propagator)), tracer.WithLogger(testutils.DiscardLogger()))
					require.NoError(t, err)
				}
				var (
					mu    sync.Mutex
					views []serverCallView
				)
				record := func(next connectrpc.ServerFunc) connectrpc.ServerFunc {
					return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
						info, _ := connectrpc.CallInfoForServerContext(ctx)
						span, _ := tracer.SpanFromContext(ctx)
						tags := span.AsMap()
						view := serverCallView{header: cloneHeader(info.RequestHeader()), traceID: span.Context().TraceID()}
						view.parentID, _ = tags[ext.MapSpanParentID].(uint64)
						view.origin, _ = tags["_dd.origin"].(string)
						mu.Lock()
						views = append(views, view)
						mu.Unlock()
						return next(ctx, spec, stream)
					}
				}
				rig := newTestRig(t, transport, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor(), record}})
				ctx, info := connectrpc.NewClientContext(context.Background())
				info.RequestHeader().Set("Authorization", "Bearer token")
				for i, call := range test.calls {
					if call.before != nil {
						call.before(info.RequestHeader())
					}
					callCtx := ctx
					var caller *tracer.Span
					if call.parent != nil {
						parent, err := tracer.Extract(call.parent)
						require.NoError(t, err)
						caller = tracer.StartSpan("caller", tracer.ChildOf(parent))
						callCtx = tracer.ContextWithSpan(ctx, caller)
					}
					values, err := call.rpc.call(callCtx, rig.client(NewClientInterceptor(call.client...)), "hello")
					require.NoError(t, err)
					assert.Equal(t, call.rpc.want, values)
					caller.Finish()

					mu.Lock()
					require.Len(t, views, i+1)
					view := views[i]
					mu.Unlock()
					assert.Equal(t, []string{"Bearer token"}, view.header.Values("Authorization"), "call %d", i+1)
					for key, value := range call.sent {
						var want []string
						if value != "" {
							want = []string{value}
						}
						assert.Equal(t, want, view.header.Values(key), "call %d: %s", i+1, key)
					}
					if call.propagated {
						assert.Equal(t, caller.Context().TraceID(), view.traceID, "call %d", i+1)
						assert.NotZero(t, view.parentID, "call %d", i+1)
						// Only a server span that continues propagated headers is tagged with their
						// origin: in process, it continues the client's span from the context.
						if _, ok := call.parent["x-datadog-origin"]; !ok {
							assert.Empty(t, view.origin, "call %d", i+1)
						}
						continue
					}
					for _, key := range traceHeaders {
						if _, ok := call.sent[key]; !ok {
							assert.Empty(t, view.header.Values(key), "call %d: %s", i+1, key)
						}
					}
					assert.Zero(t, view.parentID, "call %d", i+1)
				}
			})
		}
	}

	// The interceptors and transport that the tracing interceptor wraps may send or copy the request
	// header while they open the stream, before the call's trace headers are injected: they see none
	// of those of an earlier call.
	for _, transport := range testTransports {
		t.Run("an inner interceptor opens the stream without earlier trace headers/"+transport.name, func(t *testing.T) {
			startMockTracer(t)
			var opened []map[string][]string
			inner := func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
				return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
					info, _ := connectrpc.CallInfoForClientContext(ctx)
					opened = append(opened, headerSnapshot(info.RequestHeader()))
					return next(ctx, spec)
				}
			}
			client := newTestRig(t, transport, rigOptions{}).client(NewClientInterceptor(), inner)
			ctx, info := connectrpc.NewClientContext(context.Background())
			info.RequestHeader().Set("Authorization", "Bearer token")
			for _, parent := range []tracer.TextMapCarrier{synthetics, upstream} {
				spanContext, err := tracer.Extract(parent)
				require.NoError(t, err)
				caller := tracer.StartSpan("caller", tracer.ChildOf(spanContext))
				values, err := unaryRPC.call(tracer.ContextWithSpan(ctx, caller), client, "hello")
				require.NoError(t, err)
				assert.Equal(t, unaryRPC.want, values)
				caller.Finish()
			}
			require.Len(t, opened, 2)
			for i, header := range opened {
				assert.Equal(t, map[string][]string{"Authorization": {"Bearer token"}}, header, "call %d", i+1)
			}
		})
	}
}
