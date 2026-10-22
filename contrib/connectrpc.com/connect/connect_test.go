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
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	unaryProcedure        = "/test.connect.v1.TestService/Unary"
	clientStreamProcedure = "/test.connect.v1.TestService/ClientStream"
	serverStreamProcedure = "/test.connect.v1.TestService/ServerStream"
	bidiProcedure         = "/test.connect.v1.TestService/Bidi"
)

var (
	bidiSpec       = connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi}
	bidiClientSpec = connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true}
)

// testProtocols are the wire protocols that end-to-end tests run over.
var testProtocols = []struct {
	name   string
	opts   []connectrpc.ClientOption
	system string // the expected rpc.system
	grpc   bool
}{
	{name: "connect", system: "connectrpc"},
	{name: "grpc", opts: []connectrpc.ClientOption{connectrpc.WithGRPC()}, system: "grpc", grpc: true},
	{name: "grpcweb", opts: []connectrpc.ClientOption{connectrpc.WithGRPCWeb()}, system: "grpc", grpc: true},
}

// streamCalls runs each type of streaming RPC to completion. Each Send and Receive creates a
// message span, including the Receive that reports the end of the stream and the one connect
// makes to check that a unary message is not followed by another.
var streamCalls = []struct {
	name, procedure, method, kind  string
	clientMessages, serverMessages int
	run                            func(*testing.T, *testRig)
}{
	{
		name: "client", procedure: clientStreamProcedure, method: "ClientStream", kind: "client_streaming",
		clientMessages: 3, serverMessages: 3,
		run: func(t *testing.T, rig *testRig) {
			stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
			require.NoError(t, stream.Send(wrapperspb.String("hello")))
			response, err := stream.CloseAndReceive()
			require.NoError(t, err)
			assert.Equal(t, "hello", response.Msg.Value)
		},
	},
	{
		name: "server", procedure: serverStreamProcedure, method: "ServerStream", kind: "server_streaming",
		clientMessages: 4, serverMessages: 4,
		run: func(t *testing.T, rig *testRig) {
			stream, err := rig.client(serverStreamProcedure).CallServerStream(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
			require.NoError(t, err)
			var values []string
			for stream.Receive() {
				values = append(values, stream.Msg().Value)
			}
			require.NoError(t, stream.Err())
			require.NoError(t, stream.Close())
			assert.Equal(t, []string{"hello-1", "hello-2"}, values)
		},
	},
	{
		name: "bidi", procedure: bidiProcedure, method: "Bidi", kind: "bidi_streaming",
		clientMessages: 3, serverMessages: 3,
		run: func(t *testing.T, rig *testRig) {
			runBidi(t, rig.client(bidiProcedure).CallBidiStream(context.Background()))
		},
	},
}

type testRig struct {
	server       *httptest.Server
	clientOpts   []Option
	protocolOpts []connectrpc.ClientOption
}

func newTestRig(t *testing.T, serverOpts, clientOpts []Option, protocolOpts ...connectrpc.ClientOption) *testRig {
	t.Helper()
	handlerOpts := []connectrpc.HandlerOption{connectrpc.WithInterceptors(NewServerInterceptor(serverOpts...))}
	mux := http.NewServeMux()
	mux.Handle(unaryProcedure, connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler, handlerOpts...))
	mux.Handle(clientStreamProcedure, connectrpc.NewClientStreamHandler(clientStreamProcedure, clientStreamHandler, handlerOpts...))
	mux.Handle(serverStreamProcedure, connectrpc.NewServerStreamHandler(serverStreamProcedure, serverStreamHandler, handlerOpts...))
	mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, bidiHandler, handlerOpts...))
	return &testRig{server: newTLSServer(t, mux), clientOpts: clientOpts, protocolOpts: protocolOpts}
}

func (r *testRig) client(procedure string) *connectrpc.Client[wrapperspb.StringValue, wrapperspb.StringValue] {
	opts := make([]connectrpc.ClientOption, 1, 1+len(r.protocolOpts))
	opts[0] = connectrpc.WithInterceptors(NewClientInterceptor(r.clientOpts...))
	opts = append(opts, r.protocolOpts...)
	return connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](r.server.Client(), r.server.URL+procedure, opts...)
}

// newTLSServer serves handler over HTTP/2, which bidirectional streams require.
func newTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// inMemoryHTTPClient serves requests with handler on the caller's goroutine, so the handler's
// context derives from the client's and no network I/O is involved. connect's AnyRequest and
// AnyResponse have unexported methods, so unary calls cannot be faked below this level.
type inMemoryHTTPClient struct{ handler http.Handler }

func (c inMemoryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func unaryHandler(_ context.Context, request *connectrpc.Request[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
	if err := testError(request.Msg.Value); err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(wrapperspb.String(request.Msg.Value)), nil
}

func clientStreamHandler(_ context.Context, stream *connectrpc.ClientStream[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
	var value string
	for stream.Receive() {
		value = stream.Msg().Value
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if err := testError(value); err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(wrapperspb.String(value)), nil
}

// failAfterOne makes serverStreamHandler fail with CodeDataLoss after sending one message.
const failAfterOne = "fail-after-one"

func serverStreamHandler(_ context.Context, request *connectrpc.Request[wrapperspb.StringValue], stream *connectrpc.ServerStream[wrapperspb.StringValue]) error {
	if err := testError(request.Msg.Value); err != nil {
		return err
	}
	for _, value := range []string{request.Msg.Value + "-1", request.Msg.Value + "-2"} {
		if err := stream.Send(wrapperspb.String(value)); err != nil {
			return err
		}
		if request.Msg.Value == failAfterOne {
			return connectrpc.NewError(connectrpc.CodeDataLoss, errors.New("data loss"))
		}
	}
	return nil
}

func bidiHandler(_ context.Context, stream *connectrpc.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
	for {
		message, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := testError(message.Value); err != nil {
			return err
		}
		if err := stream.Send(wrapperspb.String(message.Value)); err != nil {
			return err
		}
	}
}

// testError returns the error the test handlers fail with when they receive value.
func testError(value string) error {
	switch value {
	case "not-found":
		return connectrpc.NewError(connectrpc.CodeNotFound, errors.New("not found"))
	case "error-details":
		return errorWithDetails(connectrpc.CodeInternal)
	case "error":
		return connectrpc.NewError(connectrpc.CodeInternal, errors.New("internal error"))
	case "coded-canceled":
		return connectrpc.NewError(connectrpc.CodeInternal, context.Canceled)
	case "eof":
		return io.EOF
	default:
		return nil
	}
}

// errorWithDetails returns an error with one detail, which WithErrorDetailTags tags as
// "seconds:1".
func errorWithDetails(code connectrpc.Code) *connectrpc.Error {
	err := connectrpc.NewError(code, errors.New("internal error"))
	detail, detailErr := connectrpc.NewErrorDetail(&durationpb.Duration{Seconds: 1})
	if detailErr != nil {
		panic(detailErr)
	}
	err.AddDetail(detail)
	return err
}

// runBidi sends one message on a bidirectional stream, receives its echo and closes the stream.
func runBidi(t *testing.T, stream *connectrpc.BidiStreamForClient[wrapperspb.StringValue, wrapperspb.StringValue]) {
	t.Helper()
	require.NoError(t, stream.Send(wrapperspb.String("hello")))
	response, err := stream.Receive()
	require.NoError(t, err)
	assert.Equal(t, "hello", response.Value)
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.CloseResponse())
}

// requireCallPair waits until exactly one client and one server call span have finished; the
// server's may finish after the client's call has returned.
func requireCallPair(t *testing.T, mt mocktracer.Tracer, kind string) (client, server *mocktracer.Span) {
	t.Helper()
	require.Eventually(t, func() bool {
		spans := mt.FinishedSpans()
		return len(spansNamed(spans, operationClient)) == 1 && len(spansNamed(spans, operationServer)) == 1
	}, 5*time.Second, time.Millisecond)
	spans := mt.FinishedSpans()
	client, server = spansNamed(spans, operationClient)[0], spansNamed(spans, operationServer)[0]
	assert.Equal(t, kind, client.Tag(tagMethodKind))
	assert.Equal(t, kind, server.Tag(tagMethodKind))
	return client, server
}

func spansNamed(spans []*mocktracer.Span, operation string) []*mocktracer.Span {
	var named []*mocktracer.Span
	for _, span := range spans {
		if span.OperationName() == operation {
			named = append(named, span)
		}
	}
	return named
}

// metadataTag returns the first value of the metadata tag for key. The tracer flattens the
// []string values into "<tag>.<index>" tags.
func metadataTag(span *mocktracer.Span, key string) any {
	return span.Tag(tagRequestMetadataPrefix + key + ".0")
}

// tagsWithPrefix returns the span's tags whose keys start with prefix.
func tagsWithPrefix(span *mocktracer.Span, prefix string) map[string]any {
	tags := make(map[string]any)
	for key, value := range span.Tags() {
		if strings.HasPrefix(key, prefix) {
			tags[key] = value
		}
	}
	return tags
}

// startAllSpanKinds starts and finishes, on each side, a call span and a message span that is its
// child, through the span-start helpers the interceptor uses.
func startAllSpanKinds(ctx context.Context, cfg *config) {
	for _, component := range []instrumentation.Component{instrumentation.ComponentClient, instrumentation.ComponentServer} {
		tags := make(map[string]any, 6)
		addProcedureTags(tags, bidiSpec)
		addProtocolTags(tags, &grpcProtocol, bidiProcedure)
		call, callCtx := cfg.startCallSpan(ctx, component, bidiProcedure, nil, tags)
		cfg.startMessageSpan(callCtx, messageTags(bidiSpec, &grpcProtocol), component).Finish()
		call.Finish()
	}
}

// requireReturns fails the test if fn does not return in time, which would mean a deadlock.
func requireReturns(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", what)
	}
}

// callOr returns fn(), or err if fn is nil. The fake connections use it for their overridable
// methods.
func callOr(fn func() error, err error) error {
	if fn != nil {
		return fn()
	}
	return err
}

func returns(err error) func() error { return func() error { return err } }

func panics(value any) func() error { return func() error { panic(value) } }

// nilConnectErrorWrapper wraps a nil *connectrpc.Error without calling any of its methods.
type nilConnectErrorWrapper struct{}

func (nilConnectErrorWrapper) Error() string { return "wrapped nil connect error" }
func (nilConnectErrorWrapper) Unwrap() error { return (*connectrpc.Error)(nil) }

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

func TestUnary(t *testing.T) {
	for _, protocol := range testProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			opts := []Option{
				WithService("connect-test"),
				WithMetadataTags(),
				WithIgnoredMetadata("X-Ignored"),
				WithRequestTags(),
				WithCustomTag("custom-tag", "custom-value"),
				WithSpanOptions(tracer.Tag("span-option", "span-value")),
			}
			rig := newTestRig(t, opts, opts, protocol.opts...)
			request := connectrpc.NewRequest(wrapperspb.String("hello"))
			for key, value := range map[string]string{
				"X-Test":            "visible",
				"X-Ignored":         "hidden",
				"Authorization":     "Bearer secret",
				"B3":                "trace-span-1",
				"Ot-Baggage-Secret": "secret",
				"Payload-Bin":       "binary",
				"X-B3-Traceid":      "trace",
			} {
				request.Header().Set(key, value)
			}
			response, err := rig.client(unaryProcedure).CallUnary(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, "hello", response.Msg.Value)

			clientSpan, serverSpan := requireCallPair(t, mt, "unary")
			assert.Equal(t, "connect.client", clientSpan.OperationName())
			assert.Equal(t, "connect.server", serverSpan.OperationName())
			assert.Equal(t, "client", clientSpan.Tag(ext.SpanKind))
			assert.Equal(t, "server", serverSpan.Tag(ext.SpanKind))
			assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
			assert.Equal(t, clientSpan.TraceID(), serverSpan.TraceID())
			for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
				assert.Equal(t, unaryProcedure, span.Tag(ext.ResourceName))
				assert.Equal(t, "test.connect.v1.TestService", span.Tag(ext.RPCService))
				assert.Equal(t, "Unary", span.Tag(ext.RPCMethod))
				assert.Equal(t, "unary", span.Tag("connect.method.kind"))
				assert.Equal(t, protocol.system, span.Tag("rpc.system"))
				assert.Equal(t, "connectrpc.com/connect", span.Tag(ext.Component))
				assert.Equal(t, "rpc", span.Tag(ext.SpanType))
				assert.Equal(t, "connect-test", span.Tag(ext.ServiceName))
				assert.Equal(t, "opt.with_service", span.Tag(ext.KeyServiceSource))
				assert.Equal(t, "custom-value", span.Tag("custom-tag"))
				assert.Equal(t, "span-value", span.Tag("span-option"))
				assert.Equal(t, "visible", span.Tag("rpc.request.metadata.x-test.0"))
				// x-datadog-trace-id is injected by the client interceptor.
				for _, key := range []string{"authorization", "b3", "ot-baggage-secret", "x-b3-traceid", "x-ignored", "payload-bin", "x-datadog-trace-id"} {
					assert.Nil(t, metadataTag(span, key), key)
				}
				assert.Nil(t, span.Tag(ext.ErrorMsg))
				assert.Nil(t, span.Tag("rpc.connect_rpc.error_code"))
				if protocol.grpc {
					assert.Equal(t, unaryProcedure, span.Tag("rpc.grpc.full_method"))
					assert.EqualValues(t, 0, span.Tag("rpc.grpc.status_code"))
					assert.Equal(t, `"hello"`, span.Tag("grpc.request"))
				} else {
					assert.Nil(t, span.Tag("rpc.grpc.full_method"))
					assert.Nil(t, span.Tag("rpc.grpc.status_code"))
					assert.Equal(t, `"hello"`, span.Tag("connect.request"))
				}
			}
			assert.Equal(t, "127.0.0.1", clientSpan.Tag(ext.NetworkDestinationIP))
			port, ok := clientSpan.Tag(ext.NetworkDestinationPort).(float64)
			assert.True(t, ok)
			assert.Positive(t, port)
			assert.Nil(t, serverSpan.Tag(ext.NetworkDestinationIP))
			assert.Nil(t, serverSpan.Tag(ext.NetworkDestinationPort))
		})
	}
}

func TestUnaryErrors(t *testing.T) {
	grpc := []connectrpc.ClientOption{connectrpc.WithGRPC()}
	for _, test := range []struct {
		name         string
		value        string // selects the handler's error, see testError
		opts         []Option
		protocolOpts []connectrpc.ClientOption
		wantCode     any // rpc.connect_rpc.error_code, or rpc.grpc.status_code over gRPC
		wantError    bool
		wantTags     map[string]any // a nil value means the tag is absent
	}{
		{name: "non-error code keeps the status", value: "not-found", opts: []Option{NonErrorCodes(connectrpc.CodeNotFound)}, wantCode: "not_found"},
		{name: "explicit code beats the cause", value: "coded-canceled", wantCode: "internal", wantError: true},
		{name: "EOF is an error", value: "eof", wantCode: "unknown", wantError: true},
		{
			name: "error check suppresses the error", value: "error", wantCode: "internal",
			opts: []Option{WithErrorCheck(func(procedure string, _ error) bool { return procedure != unaryProcedure })},
		},
		{
			name: "error details", value: "error-details", opts: []Option{WithErrorDetailTags(), NoDebugStack()},
			wantCode: "internal", wantError: true,
			wantTags: map[string]any{"connect.status_details._0": "seconds:1", ext.ErrorHandlingStack: nil},
		},
		{
			name: "gRPC error details", value: "error-details", opts: []Option{WithErrorDetailTags(), NoDebugStack()}, protocolOpts: grpc,
			wantCode: uint32(connectrpc.CodeInternal), wantError: true,
			wantTags: map[string]any{"grpc.status_details._0": "seconds:1", "connect.status_details._0": nil, ext.ErrorHandlingStack: nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			rig := newTestRig(t, test.opts, test.opts, test.protocolOpts...)
			_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String(test.value)))
			require.Error(t, err)
			codeTag, otherCodeTag := tagConnectErrorCode, tagGRPCStatusCode
			if test.protocolOpts != nil {
				codeTag, otherCodeTag = otherCodeTag, codeTag
			}
			clientSpan, serverSpan := requireCallPair(t, mt, "unary")
			for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
				kind := span.Tag(ext.SpanKind)
				assert.EqualValues(t, test.wantCode, span.Tag(codeTag), kind)
				assert.Nil(t, span.Tag(otherCodeTag), kind)
				if test.wantError {
					assert.NotNil(t, span.Tag(ext.ErrorMsg), kind)
				} else {
					assert.Nil(t, span.Tag(ext.ErrorMsg), kind)
				}
				for key, value := range test.wantTags {
					assert.Equal(t, value, span.Tag(key), "%s %s", kind, key)
				}
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
	for _, call := range streamCalls {
		for _, protocol := range testProtocols {
			for _, mode := range modes {
				t.Run(call.name+"/"+protocol.name+"/"+mode.name, func(t *testing.T) {
					mt := mocktracer.Start()
					defer mt.Stop()
					// The service tells the sides apart.
					rig := newTestRig(t,
						append([]Option{WithService("server")}, mode.opts...),
						append([]Option{WithService("client")}, mode.opts...),
						protocol.opts...)
					call.run(t, rig)

					want := map[string]int{}
					if mode.calls {
						want["connect.client/client"], want["connect.server/server"] = 1, 1
					}
					if mode.messages {
						want["connect.message/client"], want["connect.message/server"] = call.clientMessages, call.serverMessages
					}
					var wantTotal int
					for _, n := range want {
						wantTotal += n
					}
					require.Eventually(t, func() bool { return len(mt.FinishedSpans()) >= wantTotal }, 5*time.Second, time.Millisecond)
					spans := mt.FinishedSpans()
					got := map[string]int{}
					callSpanIDs := map[any]uint64{}
					for _, span := range spans {
						got[fmt.Sprint(span.OperationName(), "/", span.Tag(ext.ServiceName))]++
						if span.OperationName() != "connect.message" {
							callSpanIDs[span.Tag(ext.ServiceName)] = span.SpanID()
						}
					}
					require.Equal(t, want, got)

					for _, span := range spans {
						side := span.Tag(ext.ServiceName)
						name := fmt.Sprint(span.OperationName(), "/", side)
						assert.Equal(t, call.procedure, span.Tag(ext.ResourceName), name)
						assert.Equal(t, "test.connect.v1.TestService", span.Tag(ext.RPCService), name)
						assert.Equal(t, call.method, span.Tag(ext.RPCMethod), name)
						assert.Equal(t, call.kind, span.Tag("connect.method.kind"), name)
						assert.Equal(t, protocol.system, span.Tag("rpc.system"), name)
						assert.Equal(t, "connectrpc.com/connect", span.Tag(ext.Component), name)
						assert.Equal(t, "rpc", span.Tag(ext.SpanType), name)
						// The normal end of a stream is not an error.
						assert.Nil(t, span.Tag(ext.ErrorMsg), "%s: %v", name, span.Tags())
						assert.Nil(t, span.Tag("rpc.connect_rpc.error_code"), name)
						if protocol.grpc {
							assert.Equal(t, call.procedure, span.Tag("rpc.grpc.full_method"), name)
							assert.EqualValues(t, 0, span.Tag("rpc.grpc.status_code"), name)
						} else {
							assert.Nil(t, span.Tag("rpc.grpc.full_method"), name)
							assert.Nil(t, span.Tag("rpc.grpc.status_code"), name)
						}
						if side == "client" {
							assert.Equal(t, "127.0.0.1", span.Tag(ext.NetworkDestinationIP), name)
						} else {
							assert.Nil(t, span.Tag(ext.NetworkDestinationIP), name)
						}
						switch {
						case span.OperationName() == "connect.client":
							assert.Equal(t, "client", span.Tag(ext.SpanKind))
							assert.Zero(t, span.ParentID())
						case span.OperationName() == "connect.server":
							assert.Equal(t, "server", span.Tag(ext.SpanKind))
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
				})
			}
		}
	}
}

// TestStreamingFailsAfterAMessage checks streams that fail after a message was exchanged: the
// call spans carry the error, and the earlier message spans don't.
func TestStreamingFailsAfterAMessage(t *testing.T) {
	for _, test := range []struct {
		name, procedure, kind string
		code                  connectrpc.Code
		run                   func(*testing.T, *testRig)
	}{
		{
			name: "server", procedure: serverStreamProcedure, kind: methodKindServerStream, code: connectrpc.CodeDataLoss,
			run: func(t *testing.T, rig *testRig) {
				stream, err := rig.client(serverStreamProcedure).CallServerStream(context.Background(), connectrpc.NewRequest(wrapperspb.String(failAfterOne)))
				require.NoError(t, err)
				require.True(t, stream.Receive())
				require.False(t, stream.Receive())
				require.Equal(t, connectrpc.CodeDataLoss, connectrpc.CodeOf(stream.Err()))
				require.NoError(t, stream.Close())
			},
		},
		{
			name: "bidi", procedure: bidiProcedure, kind: methodKindBidiStream, code: connectrpc.CodeInternal,
			run: func(t *testing.T, rig *testRig) {
				stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				_, err := stream.Receive()
				require.NoError(t, err)
				require.NoError(t, stream.Send(wrapperspb.String("error")))
				_, err = stream.Receive()
				require.Equal(t, connectrpc.CodeInternal, connectrpc.CodeOf(err))
				require.NoError(t, stream.CloseRequest())
				require.NoError(t, stream.CloseResponse())
			},
		},
	} {
		for _, protocol := range testProtocols {
			t.Run(test.name+"/"+protocol.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				rig := newTestRig(t, []Option{WithService("server")}, []Option{WithService("client")}, protocol.opts...)
				test.run(t, rig)
				clientSpan, serverSpan := requireCallPair(t, mt, test.kind)
				for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
					assert.NotNil(t, span.Tag(ext.ErrorMsg), span.OperationName())
					if protocol.grpc {
						assert.EqualValues(t, test.code, span.Tag(tagGRPCStatusCode), span.OperationName())
					} else {
						assert.Equal(t, test.code.String(), span.Tag(tagConnectErrorCode), span.OperationName())
					}
				}
				// Only the client's last Receive, which reports the error, fails.
				var failed []*mocktracer.Span
				messages := spansNamed(mt.FinishedSpans(), operationMessage)
				for _, span := range messages {
					if span.Tag(ext.ErrorMsg) != nil {
						failed = append(failed, span)
					}
				}
				require.Len(t, failed, 1, "%d message spans", len(messages))
				assert.Equal(t, "client", failed[0].Tag(ext.ServiceName))
			})
		}
	}
}

// TestStreamingConcurrentBidi sends and receives on a bidi stream from two goroutines, as connect
// allows.
func TestStreamingConcurrentBidi(t *testing.T) {
	const iterations, messages = 10, 20
	for _, protocol := range testProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			opts := []Option{WithMetadataTags(), WithRequestTags()}
			rig := newTestRig(t, append([]Option{WithService("server")}, opts...), append([]Option{WithService("client")}, opts...), protocol.opts...)
			for range iterations {
				stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
				var wg sync.WaitGroup
				wg.Go(func() {
					defer func() { assert.NoError(t, stream.CloseResponse()) }()
					for {
						_, err := stream.Receive()
						if errors.Is(err, io.EOF) {
							return
						}
						if !assert.NoError(t, err) {
							return
						}
					}
				})
				wg.Go(func() {
					stream.RequestHeader().Set("X-Concurrent", "value")
					for range messages {
						if !assert.NoError(t, stream.Send(wrapperspb.String("hello"))) {
							return
						}
					}
					assert.NoError(t, stream.CloseRequest())
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
			if protocol.grpc {
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
	}
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
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			parent := tracer.StartSpan("parent")
			opts := []Option{WithStreamCalls(false), WithRequestTags(), WithMetadataTags()}
			if test.spanOption {
				opts = append(opts, WithSpanOptions(tracer.ChildOf(parent.Context())))
			}
			ctx := context.Background()
			if test.ambient {
				ctx = tracer.ContextWithSpan(ctx, parent)
			}
			rig := newTestRig(t, slices.Concat(opts, []Option{WithService("server")}), slices.Concat(opts, []Option{WithService("client")}))
			stream := rig.client(bidiProcedure).CallBidiStream(ctx)
			stream.RequestHeader().Set("X-Message-Header", "value")
			runBidi(t, stream)
			parent.Finish()

			spans := mt.FinishedSpans()
			assert.Empty(t, spansNamed(spans, operationClient))
			assert.Empty(t, spansNamed(spans, operationServer))
			messages := spansNamed(spans, operationMessage)
			require.Len(t, messages, 6)
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
					assert.Equal(t, "127.0.0.1", span.Tag(ext.NetworkDestinationIP))
				} else {
					assert.Equal(t, test.wantServerKind, span.Tag(ext.SpanKind))
				}
				if value := metadataTag(span, "x-message-header"); value != nil {
					assert.Equal(t, "value", value)
					metadataTagged[side]++
				}
				if value := span.Tag(tagConnectRequest); value != nil {
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

// TestStartTimeTags checks the tags that samplers and span options see when a span starts. A
// streaming client only learns its protocol once connected, after its call span has started.
func TestStartTimeTags(t *testing.T) {
	for _, protocol := range testProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var (
				mu     sync.Mutex
				starts []map[string]any
			)
			capture := WithSpanOptions(func(cfg *tracer.StartSpanConfig) {
				mu.Lock()
				defer mu.Unlock()
				starts = append(starts, maps.Clone(cfg.Tags))
			})
			rig := newTestRig(t, []Option{capture}, []Option{capture}, protocol.opts...)
			_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
			require.NoError(t, err)
			runBidi(t, rig.client(bidiProcedure).CallBidiStream(context.Background()))
			require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 10 }, 5*time.Second, time.Millisecond)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, starts, 10)
			for _, tags := range starts {
				assert.Contains(t, []any{unaryProcedure, bidiProcedure}, tags[ext.ResourceName])
				assert.Equal(t, "test.connect.v1.TestService", tags[ext.RPCService])
				assert.Contains(t, []any{"Unary", "Bidi"}, tags[ext.RPCMethod])
				assert.Contains(t, []any{"unary", "bidi_streaming"}, tags[tagMethodKind])
				if tags[ext.SpanKind] == ext.SpanKindClient && tags[tagMethodKind] == "bidi_streaming" {
					assert.NotContains(t, tags, ext.RPCSystem)
					assert.NotContains(t, tags, ext.GRPCFullMethod)
					continue
				}
				assert.Equal(t, protocol.system, tags[ext.RPCSystem], tags)
				if protocol.grpc {
					assert.Equal(t, tags[ext.ResourceName], tags[ext.GRPCFullMethod])
				} else {
					assert.NotContains(t, tags, ext.GRPCFullMethod)
				}
			}
		})
	}
}

// TestPassThrough checks the calls an interceptor must not trace: a call on the side it does not
// trace, or to an untraced procedure. Such calls must not inject the caller's span either, except
// when a stream is only untraced because both of its span types are disabled.
func TestPassThrough(t *testing.T) {
	for _, test := range []struct {
		name           string
		client, server func() connectrpc.Interceptor
		// inner adds a default interceptor after the tested one on each side, as Orchestrion does,
		// which must leave the call untraced too.
		inner        bool
		procedure    string
		wantInjected bool
	}{
		{name: "server interceptor on a client/unary", client: func() connectrpc.Interceptor { return NewServerInterceptor() }, procedure: unaryProcedure},
		{name: "server interceptor on a client/bidi", client: func() connectrpc.Interceptor { return NewServerInterceptor() }, procedure: bidiProcedure},
		{name: "client interceptor on a handler/unary", server: func() connectrpc.Interceptor { return NewClientInterceptor() }, procedure: unaryProcedure},
		{name: "client interceptor on a handler/bidi", server: func() connectrpc.Interceptor { return NewClientInterceptor() }, procedure: bidiProcedure},
		{
			name: "untraced method/unary", procedure: unaryProcedure,
			client: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(unaryProcedure)) },
			server: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(unaryProcedure)) },
		},
		{
			name: "untraced method/bidi", procedure: bidiProcedure,
			client: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(bidiProcedure)) },
			server: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(bidiProcedure)) },
		},
		{
			name: "no call or message spans/bidi", procedure: bidiProcedure, wantInjected: true,
			client: func() connectrpc.Interceptor {
				return NewInterceptor(WithStreamCalls(false), WithStreamMessages(false))
			},
			server: func() connectrpc.Interceptor {
				return NewInterceptor(WithStreamCalls(false), WithStreamMessages(false))
			},
		},
		{
			name: "untraced method with an inner interceptor/unary", procedure: unaryProcedure, inner: true,
			client: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(unaryProcedure)) },
			server: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(unaryProcedure)) },
		},
		{
			name: "untraced method with an inner interceptor/bidi", procedure: bidiProcedure, inner: true,
			client: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(bidiProcedure)) },
			server: func() connectrpc.Interceptor { return NewInterceptor(WithUntracedMethods(bidiProcedure)) },
		},
		{
			name: "no call or message spans with an inner interceptor/bidi", procedure: bidiProcedure, inner: true, wantInjected: true,
			client: func() connectrpc.Interceptor {
				return NewInterceptor(WithStreamCalls(false), WithStreamMessages(false))
			},
			server: func() connectrpc.Interceptor {
				return NewInterceptor(WithStreamCalls(false), WithStreamMessages(false))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var handlerOpts []connectrpc.HandlerOption
			if test.server != nil {
				handlerOpts = append(handlerOpts, connectrpc.WithInterceptors(test.server()))
			}
			var clientOpts []connectrpc.ClientOption
			if test.client != nil {
				clientOpts = append(clientOpts, connectrpc.WithInterceptors(test.client()))
			}
			if test.inner {
				handlerOpts = append(handlerOpts, connectrpc.WithInterceptors(NewInterceptor()))
				clientOpts = append(clientOpts, connectrpc.WithInterceptors(NewInterceptor()))
			}
			type received struct {
				traceID string
				wrapped bool
			}
			calls := make(chan received, 1)
			mux := http.NewServeMux()
			mux.Handle(unaryProcedure, connectrpc.NewUnaryHandler(unaryProcedure,
				func(_ context.Context, request *connectrpc.Request[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
					calls <- received{traceID: request.Header().Get(tracer.DefaultTraceIDHeader)}
					return connectrpc.NewResponse(request.Msg), nil
				}, handlerOpts...))
			mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure,
				func(ctx context.Context, stream *connectrpc.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
					_, wrapped := stream.Conn().(*streamingHandlerConn)
					calls <- received{traceID: stream.RequestHeader().Get(tracer.DefaultTraceIDHeader), wrapped: wrapped}
					return bidiHandler(ctx, stream)
				}, handlerOpts...))
			server := newTLSServer(t, mux)
			client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+test.procedure, clientOpts...)

			parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
			if test.procedure == unaryProcedure {
				_, err := client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("hello")))
				require.NoError(t, err)
			} else {
				stream := client.CallBidiStream(ctx)
				conn, err := stream.Conn()
				require.NoError(t, err)
				_, wrapped := conn.(*streamingClientConn)
				assert.False(t, wrapped, "client stream is wrapped")
				runBidi(t, stream)
			}
			parent.Finish()

			got := <-calls
			assert.False(t, got.wrapped, "handler stream is wrapped")
			assert.Equal(t, test.wantInjected, got.traceID != "", "trace context injected")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, "parent", spans[0].OperationName())
		})
	}
}

func TestDoubleInstrumentationIsDeduplicated(t *testing.T) {
	const sideProcedure = "/test.connect.v1.TestService/Side"
	newHandler := func(interceptors ...connectrpc.Interceptor) http.Handler {
		opts := connectrpc.WithInterceptors(interceptors...)
		mux := http.NewServeMux()
		mux.Handle(unaryProcedure, connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler, opts))
		mux.Handle(sideProcedure, connectrpc.NewUnaryHandler(sideProcedure, unaryHandler, opts))
		mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, bidiHandler, opts))
		return mux
	}
	callUnary := func(t *testing.T, httpClient connectrpc.HTTPClient, baseURL string, opts ...connectrpc.ClientOption) {
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](httpClient, baseURL+unaryProcedure, opts...)
		_, err := client.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		require.NoError(t, err)
	}
	callBidi := func(t *testing.T, httpClient connectrpc.HTTPClient, baseURL string, opts ...connectrpc.ClientOption) {
		client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](httpClient, baseURL+bidiProcedure, opts...)
		runBidi(t, client.CallBidiStream(context.Background()))
	}
	byResource := func(t *testing.T, spans []*mocktracer.Span, resource string) *mocktracer.Span {
		t.Helper()
		for _, span := range spans {
			if span.Tag(ext.ResourceName) == resource {
				return span
			}
		}
		t.Fatalf("no span for %s", resource)
		return nil
	}
	for _, test := range []struct {
		name        string
		call        func(*testing.T, connectrpc.HTTPClient, string, ...connectrpc.ClientOption)
		kind        string
		serverTwice bool
		clientOpts  func() []connectrpc.ClientOption
	}{
		{name: "chained client interceptors/unary", call: callUnary, kind: methodKindUnary, clientOpts: func() []connectrpc.ClientOption {
			return []connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor(), NewClientInterceptor())}
		}},
		{name: "nested client options/unary", call: callUnary, kind: methodKindUnary, clientOpts: func() []connectrpc.ClientOption {
			return []connectrpc.ClientOption{connectrpc.WithClientOptions(
				connectrpc.WithClientOptions(connectrpc.WithInterceptors(NewClientInterceptor())),
				connectrpc.WithInterceptors(NewClientInterceptor()),
			)}
		}},
		{name: "chained client interceptors/bidi", call: callBidi, kind: methodKindBidiStream, clientOpts: func() []connectrpc.ClientOption {
			return []connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor(), NewInterceptor())}
		}},
		{name: "chained server interceptors/unary", call: callUnary, kind: methodKindUnary, serverTwice: true, clientOpts: func() []connectrpc.ClientOption {
			return []connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor())}
		}},
		{name: "chained server interceptors/bidi", call: callBidi, kind: methodKindBidiStream, serverTwice: true, clientOpts: func() []connectrpc.ClientOption {
			return []connectrpc.ClientOption{connectrpc.WithInterceptors(NewClientInterceptor())}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			interceptors := []connectrpc.Interceptor{NewServerInterceptor()}
			if test.serverTwice {
				interceptors = append(interceptors, NewInterceptor())
			}
			server := newTLSServer(t, newHandler(interceptors...))
			test.call(t, server.Client(), server.URL, test.clientOpts()...)
			clientSpan, serverSpan := requireCallPair(t, mt, test.kind)
			assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
			assert.Len(t, spansNamed(mt.FinishedSpans(), operationClient), 1)
			assert.Len(t, spansNamed(mt.FinishedSpans(), operationServer), 1)
		})
	}

	// An outer interceptor that traces a stream without a call span still handles it.
	t.Run("chained interceptors without call spans/bidi", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		server := newTLSServer(t, newHandler(NewServerInterceptor(WithStreamCalls(false)), NewServerInterceptor()))
		callBidi(t, server.Client(), server.URL, connectrpc.WithInterceptors(NewClientInterceptor(WithStreamCalls(false)), NewClientInterceptor()))
		// Each side sends one message and receives it, then the end of the stream.
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) >= 6 }, 5*time.Second, time.Millisecond)
		spans := mt.FinishedSpans()
		assert.Len(t, spansNamed(spans, operationMessage), 6)
		assert.Len(t, spans, 6)
	})

	// An RPC that an interceptor makes with the context of the call it intercepts is another call,
	// which is traced and propagated.
	for _, test := range []struct {
		name      string
		call      func(*testing.T, connectrpc.HTTPClient, string, ...connectrpc.ClientOption)
		procedure string
		// sideStream makes the side call a bidi stream, rather than a unary call to sideProcedure.
		sideStream   bool
		interceptors func(side connectrpc.Interceptor) []connectrpc.Interceptor
	}{
		{
			name: "inner interceptor makes a unary call/unary", call: callUnary, procedure: unaryProcedure,
			interceptors: func(side connectrpc.Interceptor) []connectrpc.Interceptor {
				return []connectrpc.Interceptor{NewClientInterceptor(), side}
			},
		},
		{
			name: "inner interceptor makes a unary call/bidi", call: callBidi, procedure: bidiProcedure,
			interceptors: func(side connectrpc.Interceptor) []connectrpc.Interceptor {
				return []connectrpc.Interceptor{NewClientInterceptor(), side}
			},
		},
		{
			name: "inner interceptor opens a stream/unary", call: callUnary, procedure: unaryProcedure, sideStream: true,
			interceptors: func(side connectrpc.Interceptor) []connectrpc.Interceptor {
				return []connectrpc.Interceptor{NewClientInterceptor(), side}
			},
		},
		{
			// Orchestrion weaves an interceptor into every connect.WithClientOptions call.
			name: "woven chain/unary", call: callUnary, procedure: unaryProcedure,
			interceptors: func(side connectrpc.Interceptor) []connectrpc.Interceptor {
				return []connectrpc.Interceptor{NewClientInterceptor(), side, NewClientInterceptor()}
			},
		},
		{
			name: "woven chain/bidi", call: callBidi, procedure: bidiProcedure,
			interceptors: func(side connectrpc.Interceptor) []connectrpc.Interceptor {
				return []connectrpc.Interceptor{NewClientInterceptor(), side, NewClientInterceptor()}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			server := newTLSServer(t, newHandler(NewServerInterceptor()))
			sideOpts := connectrpc.WithInterceptors(NewClientInterceptor())
			sideResource := sideProcedure
			if test.sideStream {
				sideResource = bidiProcedure
			}
			side := sideCallInterceptor{call: func(ctx context.Context) {
				client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+sideResource, sideOpts)
				if test.sideStream {
					runBidi(t, client.CallBidiStream(ctx))
					return
				}
				_, err := client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("side")))
				require.NoError(t, err)
			}}
			test.call(t, server.Client(), server.URL, connectrpc.WithInterceptors(test.interceptors(side)...))

			require.Eventually(t, func() bool {
				spans := mt.FinishedSpans()
				return len(spansNamed(spans, operationClient)) >= 2 && len(spansNamed(spans, operationServer)) >= 2
			}, 5*time.Second, time.Millisecond)
			clients, servers := spansNamed(mt.FinishedSpans(), operationClient), spansNamed(mt.FinishedSpans(), operationServer)
			require.Len(t, clients, 2)
			require.Len(t, servers, 2)
			mainClient, sideClient := byResource(t, clients, test.procedure), byResource(t, clients, sideResource)
			mainServer, sideServer := byResource(t, servers, test.procedure), byResource(t, servers, sideResource)
			assert.Equal(t, mainClient.SpanID(), sideClient.ParentID())
			assert.Equal(t, mainClient.SpanID(), mainServer.ParentID())
			assert.Equal(t, sideClient.SpanID(), sideServer.ParentID())
		})
	}

	for _, test := range []struct {
		name string
		// innerClientTraced adds a client interceptor to the client that the outer handler uses.
		innerClientTraced bool
	}{
		{name: "in-memory transport and outbound call from handler", innerClientTraced: true},
		{name: "in-memory transport from handler without a client interceptor"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			const outerProcedure = "/test.connect.v1.TestService/Outer"
			var innerOpts []connectrpc.ClientOption
			if test.innerClientTraced {
				innerOpts = append(innerOpts, connectrpc.WithInterceptors(NewClientInterceptor()))
			}
			inner := inMemoryHTTPClient{handler: newHandler(NewServerInterceptor())}
			innerClient := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](inner, "http://inner"+unaryProcedure, innerOpts...)
			mux := http.NewServeMux()
			mux.Handle(outerProcedure, connectrpc.NewUnaryHandler(outerProcedure,
				func(ctx context.Context, request *connectrpc.Request[wrapperspb.StringValue]) (*connectrpc.Response[wrapperspb.StringValue], error) {
					return innerClient.CallUnary(ctx, connectrpc.NewRequest(request.Msg))
				},
				connectrpc.WithInterceptors(NewServerInterceptor())))
			outerClient := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](inMemoryHTTPClient{handler: mux}, "http://outer"+outerProcedure,
				connectrpc.WithInterceptors(NewClientInterceptor()))

			_, err := outerClient.CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
			require.NoError(t, err)

			clients := spansNamed(mt.FinishedSpans(), operationClient)
			servers := spansNamed(mt.FinishedSpans(), operationServer)
			require.Len(t, servers, 2)
			outerServerSpan, innerServerSpan := byResource(t, servers, outerProcedure), byResource(t, servers, unaryProcedure)
			assert.Equal(t, byResource(t, clients, outerProcedure).SpanID(), outerServerSpan.ParentID())
			if !test.innerClientTraced {
				require.Len(t, clients, 1)
				assert.Equal(t, outerServerSpan.SpanID(), innerServerSpan.ParentID())
				return
			}
			require.Len(t, clients, 2)
			innerClientSpan := byResource(t, clients, unaryProcedure)
			assert.Equal(t, outerServerSpan.SpanID(), innerClientSpan.ParentID())
			assert.Equal(t, innerClientSpan.SpanID(), innerServerSpan.ParentID())
		})
	}

	// A call to the same procedure through another client, as failover or mirroring interceptors
	// make, is a unary call of its own. A stream has no per-call identity, so it is passed through,
	// but it stays in the trace.
	for _, test := range []struct {
		name   string
		stream bool
		// ownSpan makes the failover interceptor start a span of its own around its call.
		ownSpan bool
	}{
		{name: "same procedure through another client/unary"},
		{name: "same procedure through another client/bidi", stream: true},
		{name: "same procedure through another client under another span/bidi", stream: true, ownSpan: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			primary := newTLSServer(t, newHandler(NewServerInterceptor()))
			secondary := newTLSServer(t, newHandler(NewServerInterceptor(WithService("secondary"))))
			procedure, call := unaryProcedure, callUnary
			if test.stream {
				procedure, call = bidiProcedure, callBidi
			}
			failover := sideCallInterceptor{call: func(ctx context.Context) {
				if test.ownSpan {
					var span *tracer.Span
					span, ctx = tracer.StartSpanFromContext(ctx, "failover")
					defer span.Finish()
				}
				client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](secondary.Client(), secondary.URL+procedure,
					connectrpc.WithInterceptors(NewClientInterceptor()))
				if test.stream {
					runBidi(t, client.CallBidiStream(ctx))
					return
				}
				_, err := client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("failover")))
				require.NoError(t, err)
			}}
			call(t, primary.Client(), primary.URL, connectrpc.WithInterceptors(NewClientInterceptor(), failover))

			isSecondary := func(span *mocktracer.Span) bool { return span.Tag(ext.ServiceName) == "secondary" }
			require.Eventually(t, func() bool { return len(spansNamed(mt.FinishedSpans(), operationServer)) >= 2 }, 5*time.Second, time.Millisecond)
			var primaryServer, secondaryServer *mocktracer.Span
			for _, span := range spansNamed(mt.FinishedSpans(), operationServer) {
				if isSecondary(span) {
					secondaryServer = span
				} else {
					primaryServer = span
				}
			}
			require.NotNil(t, primaryServer)
			require.NotNil(t, secondaryServer)
			clients := spansNamed(mt.FinishedSpans(), operationClient)
			if test.ownSpan {
				// A span started in between makes it another call, which is traced.
				require.Len(t, clients, 2)
				userSpan := spansNamed(mt.FinishedSpans(), "failover")
				require.Len(t, userSpan, 1)
				for _, client := range clients {
					if client.SpanID() == secondaryServer.ParentID() {
						assert.Equal(t, userSpan[0].SpanID(), client.ParentID())
						return
					}
				}
				t.Fatal("the secondary server span is not a child of a client span")
			}
			if test.stream {
				require.Len(t, clients, 1)
				assert.Equal(t, clients[0].SpanID(), secondaryServer.ParentID())
				assert.Equal(t, clients[0].SpanID(), primaryServer.ParentID())
				return
			}
			require.Len(t, clients, 2)
			mainClient, failoverClient := clients[0], clients[1]
			if failoverClient.SpanID() == mainClient.ParentID() {
				mainClient, failoverClient = failoverClient, mainClient
			}
			assert.Equal(t, mainClient.SpanID(), failoverClient.ParentID())
			assert.Equal(t, failoverClient.SpanID(), secondaryServer.ParentID())
			assert.Equal(t, mainClient.SpanID(), primaryServer.ParentID())
		})
	}
}

// sideCallInterceptor makes another RPC, with the intercepted call's context, before each client
// call.
type sideCallInterceptor struct{ call func(context.Context) }

func (i sideCallInterceptor) WrapUnary(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
	return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
		i.call(ctx)
		return next(ctx, request)
	}
}

func (i sideCallInterceptor) WrapStreamingClient(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
	return func(ctx context.Context, spec connectrpc.Spec) connectrpc.StreamingClientConn {
		i.call(ctx)
		return next(ctx, spec)
	}
}

func (sideCallInterceptor) WrapStreamingHandler(next connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
	return next
}

func TestFinishSpan(t *testing.T) {
	var (
		typedNilConnectErr *connectrpc.Error
		call               = finishMode{}
		message            = messageFinishMode
		panicked           = finishMode{isPanic: true}
		unaryGET           = unaryFinishMode(&connectProtocol, http.MethodGet)
		boom               = connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom"))
	)
	unaryGETPanic := unaryGET
	unaryGETPanic.isPanic = true
	for _, test := range []struct {
		name     string
		err      error
		protocol string // connectrpc.ProtocolConnect if empty
		mode     finishMode
		opts     []Option
		// wantCode is the protocol's status tag, or nil if the tag must be absent.
		wantCode any
		// wantError is a substring of the recorded error; empty if no error may be recorded.
		wantError string
		want304   bool
		wantTags  map[string]any // a nil value means the tag is absent
	}{
		{name: "nil error", mode: call},
		{name: "nil error over gRPC", protocol: connectrpc.ProtocolGRPC, mode: call, wantCode: 0},
		{name: "error", err: boom, mode: call, wantCode: "internal", wantError: "boom"},
		{name: "error over gRPC", err: boom, protocol: connectrpc.ProtocolGRPC, mode: call, wantCode: uint32(connectrpc.CodeInternal), wantError: "boom"},
		{name: "error over gRPC-Web", err: boom, protocol: connectrpc.ProtocolGRPCWeb, mode: call, wantCode: uint32(connectrpc.CodeInternal), wantError: "boom"},
		{name: "error over an unknown protocol", err: boom, protocol: "future-protocol", mode: call, wantCode: "internal", wantError: "boom"},
		{
			name: "error stack", err: boom, mode: call, wantCode: "internal", wantError: "boom",
			wantTags: map[string]any{ext.ErrorHandlingStack: assert.ValueAssertionFunc(assert.NotNil)},
		},
		{
			name: "NoDebugStack", err: boom, mode: call, opts: []Option{NoDebugStack()}, wantCode: "internal", wantError: "boom",
			wantTags: map[string]any{ext.ErrorHandlingStack: nil, ext.ErrorStack: nil},
		},

		// End of stream.
		{name: "EOF ends a message stream", err: io.EOF, mode: message},
		{name: "EOF ends a gRPC message stream", err: io.EOF, protocol: connectrpc.ProtocolGRPC, mode: message, wantCode: 0},
		{name: "connect end-of-stream sentinel", err: connectrpc.NewError(connectrpc.CodeUnknown, io.EOF), mode: message},
		{name: "wrapped EOF ends a message stream", err: fmt.Errorf("read: %w", io.EOF), mode: message},
		{name: "coded EOF is a message error", err: connectrpc.NewError(connectrpc.CodeInternal, io.EOF), mode: message, wantCode: "internal", wantError: "EOF"},
		{name: "EOF is a call error", err: io.EOF, mode: call, wantCode: "unknown", wantError: "EOF"},

		// Not modified.
		{name: "not modified GET", err: connectrpc.NewNotModifiedError(nil), mode: unaryGET, want304: true},
		{name: "not modified POST", err: connectrpc.NewNotModifiedError(nil), mode: unaryFinishMode(&connectProtocol, http.MethodPost), wantCode: "unknown", wantError: "not modified"},
		{
			name: "not modified over gRPC", err: connectrpc.NewNotModifiedError(nil), protocol: connectrpc.ProtocolGRPC,
			mode: unaryFinishMode(&grpcProtocol, http.MethodGet), wantCode: uint32(connectrpc.CodeUnknown), wantError: "not modified",
		},
		{
			name: "not modified over gRPC-Web", err: connectrpc.NewNotModifiedError(nil), protocol: connectrpc.ProtocolGRPCWeb,
			mode: unaryFinishMode(protocolTagsFor(connectrpc.ProtocolGRPCWeb), http.MethodGet), wantCode: uint32(connectrpc.CodeUnknown), wantError: "not modified",
		},

		// Suppression.
		{name: "uncoded cancellation is never an error", err: context.Canceled, mode: call, opts: []Option{NonErrorCodes()}, wantCode: "canceled"},
		{name: "coded cancellation is suppressed by default", err: connectrpc.NewError(connectrpc.CodeCanceled, context.Canceled), mode: call, wantCode: "canceled"},
		{
			name: "coded cancellation can be an error", err: connectrpc.NewError(connectrpc.CodeCanceled, context.Canceled), mode: call,
			opts: []Option{NonErrorCodes()}, wantCode: "canceled", wantError: "canceled",
		},
		{name: "explicit code beats the cause", err: connectrpc.NewError(connectrpc.CodeInternal, context.Canceled), mode: call, wantCode: "internal", wantError: "canceled"},
		{name: "NonErrorCodes keeps the status", err: connectrpc.NewError(connectrpc.CodeNotFound, errors.New("nope")), mode: call, opts: []Option{NonErrorCodes(connectrpc.CodeNotFound)}, wantCode: "not_found"},
		{
			name: "error check can suppress an error", err: boom, mode: call, wantCode: "internal",
			opts: []Option{WithErrorCheck(func(procedure string, err error) bool { return procedure != unaryProcedure || !errors.Is(err, boom) })},
		},

		// Panics are always recorded.
		{name: "panic with a cancellation", err: context.Canceled, mode: panicked, wantCode: "canceled", wantError: "canceled"},
		{name: "panic with a suppressed code", err: connectrpc.NewError(connectrpc.CodeCanceled, context.Canceled), mode: panicked, wantCode: "canceled", wantError: "canceled"},
		{name: "panic with not modified", err: connectrpc.NewNotModifiedError(nil), mode: unaryGETPanic, wantCode: "unknown", wantError: "not modified"},
		{name: "panic in a message span", err: context.Canceled, mode: finishMode{allowEOF: true, isPanic: true}, wantCode: "canceled", wantError: "canceled"},
		{name: "panic with EOF in a message span", err: io.EOF, mode: finishMode{allowEOF: true, isPanic: true}, wantCode: "unknown", wantError: "EOF"},
		{
			name: "panic bypasses NonErrorCodes and the error check", err: connectrpc.NewError(connectrpc.CodeNotFound, errors.New("boom")), mode: panicked,
			opts:     []Option{NonErrorCodes(connectrpc.CodeNotFound), WithErrorCheck(func(string, error) bool { return false })},
			wantCode: "not_found", wantError: "boom",
		},

		// Errors that panic when inspected.
		{name: "typed nil connect error", err: typedNilConnectErr, mode: call, wantCode: "unknown", wantError: "unsafe to inspect"},
		{name: "typed nil custom error", err: (*derefError)(nil), mode: call, wantCode: "unknown", wantError: "unsafe to inspect"},
		{
			name: "nil connect error in the chain", err: nilConnectErrorWrapper{}, mode: unaryGET, opts: []Option{WithErrorDetailTags()},
			wantCode: "unknown", wantError: "unsafe to inspect",
		},
		{name: "typed nil panic value", err: panicError(typedNilConnectErr), mode: panicked, wantCode: "unknown", wantError: "panic"},
		{name: "typed nil message error", err: typedNilConnectErr, mode: message, wantCode: "unknown", wantError: "unsafe to inspect"},
		{name: "wrapped typed nil custom error", err: wrappedDerefError, mode: message, wantCode: "unknown", wantError: "unsafe to inspect"},

		// Error details.
		{
			name: "error details", err: errorWithDetails(connectrpc.CodeInternal), mode: call, opts: []Option{WithErrorDetailTags()},
			wantCode: "internal", wantError: "internal error", wantTags: map[string]any{"connect.status_details._0": "seconds:1"},
		},
		{
			name: "gRPC error details", err: errorWithDetails(connectrpc.CodeInternal), protocol: connectrpc.ProtocolGRPC, mode: call,
			opts: []Option{WithErrorDetailTags()}, wantCode: uint32(connectrpc.CodeInternal), wantError: "internal error",
			wantTags: map[string]any{"grpc.status_details._0": "seconds:1"},
		},
		{
			name: "no error details for a suppressed error", err: errorWithDetails(connectrpc.CodeInternal), mode: call,
			opts: []Option{WithErrorDetailTags(), NonErrorCodes(connectrpc.CodeInternal)}, wantCode: "internal",
			wantTags: map[string]any{"connect.status_details._0": nil},
		},
		{
			name: "no error details by default", err: errorWithDetails(connectrpc.CodeInternal), mode: call,
			wantCode: "internal", wantError: "internal error", wantTags: map[string]any{"connect.status_details._0": nil},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			protocol := test.protocol
			if protocol == "" {
				protocol = connectrpc.ProtocolConnect
			}
			tags := protocolTagsFor(protocol)
			span := tracer.StartSpan("finish")
			require.NotPanics(t, func() {
				finishSpan(span, test.err, unaryProcedure, tags, test.mode, newConfig(test.opts...))
			})
			require.Len(t, mt.FinishedSpans(), 1)
			finished := mt.FinishedSpans()[0]
			assert.EqualValues(t, test.wantCode, finished.Tag(tags.statusCodeTag))
			other := &grpcProtocol
			if tags == other {
				other = &connectProtocol
			}
			assert.Nil(t, finished.Tag(other.statusCodeTag))
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
			finishSpan(nil, errors.New("boom"), unaryProcedure, &connectProtocol, finishMode{}, newConfig())
		})
	})
}

func TestSpanBases(t *testing.T) {
	bases := func(cfg *config) map[string]*tracer.StartSpanConfig {
		return map[string]*tracer.StartSpanConfig{
			"client call":    cfg.client.call,
			"client message": cfg.client.message,
			"server call":    cfg.server.call,
			"server message": cfg.server.message,
		}
	}

	for _, test := range []struct {
		name      string
		analytics bool
	}{
		{name: "hold only static tags"},
		{name: "hold the analytics rate", analytics: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.analytics {
				t.Setenv("DD_TRACE_CONNECT_ANALYTICS_ENABLED", "true")
			}
			// Per-span and user-provided values must stay out of the shared bases.
			cfg := newConfig(WithService("svc"), WithCustomTag("custom-tag", "v"), WithSpanOptions(tracer.Tag("span-option", "v")))
			static := map[string]any{ext.SpanType: "rpc", ext.Component: "connectrpc.com/connect"}
			want := map[string]map[string]any{
				"client call":    {ext.SpanKind: "client", "_dd.measured": 1},
				"client message": {},
				"server call":    {ext.SpanKind: "server", "_dd.measured": 1},
				"server message": {"_dd.measured": 1},
			}
			for name, base := range bases(cfg) {
				wantTags := maps.Clone(static)
				maps.Copy(wantTags, want[name])
				if test.analytics {
					wantTags[ext.EventSampleRate] = 1.0
				}
				assert.Equal(t, wantTags, base.Tags, name)
				// WithStartSpanConfig copies these fields into every span it starts.
				assert.Nil(t, base.Parent, name)
				assert.Nil(t, base.SpanLinks, name)
				assert.Nil(t, base.Context, name)
				assert.Zero(t, base.SpanID, name)
				assert.True(t, base.StartTime.IsZero(), name)
			}
		})
	}

	t.Run("are not mutated by spans", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := newConfig(
			WithCustomTag("custom-tag", "v"),
			WithSpanOptions(tracer.Tag("span-option", "v"), tracer.ResourceName("from-span-option")),
		)
		before := map[string]map[string]any{}
		for name, base := range bases(cfg) {
			before[name] = maps.Clone(base.Tags)
		}
		for range 3 {
			startAllSpanKinds(context.Background(), cfg)
		}
		for name, base := range bases(cfg) {
			assert.Equal(t, before[name], base.Tags, name)
		}
		require.Len(t, mt.FinishedSpans(), 12)
	})

	t.Run("are safe for concurrent use", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := newConfig(WithCustomTag("custom-tag", "v"), WithSpanOptions(tracer.Tag("span-option", "v")))
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 50 {
					startAllSpanKinds(context.Background(), cfg)
				}
			})
		}
		wg.Wait()
		assert.Len(t, mt.FinishedSpans(), 8*50*4)
	})

	// A stream's Send and Receive may run concurrently and share the stream's message tags.
	t.Run("share message tags safely", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := newConfig(WithSpanOptions(tracer.Tag("span-option", "v")), WithCustomTag("custom-tag", "v"))
		shared := messageTags(bidiSpec, &connectProtocol)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 100 {
					span := cfg.startMessageSpan(context.Background(), shared, instrumentation.ComponentServer)
					span.SetTag("per-span", "x")
					span.Finish()
				}
			})
		}
		wg.Wait()
		spans := mt.FinishedSpans()
		require.Len(t, spans, 800)
		for _, span := range spans {
			assert.Equal(t, bidiProcedure, span.Tag(ext.ResourceName))
			assert.Equal(t, "connectrpc", span.Tag(ext.RPCSystem))
		}
	})

	// The tracer drops _dd.measured from top-level spans, so the parent has the same service.
	t.Run("measure call spans and server message spans", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent", tracer.ServiceName("svc"))
		startAllSpanKinds(ctx, newConfig(WithService("svc")))
		parent.Finish()
		spans := mt.FinishedSpans()
		require.Len(t, spans, 5)
		clientCall := spansNamed(spans, operationClient)[0]
		for _, span := range spans {
			switch {
			case span.OperationName() == "parent":
			case span.ParentID() == clientCall.SpanID():
				assert.Nil(t, span.Tag("_dd.measured"), "client message")
			default:
				assert.EqualValues(t, 1, span.Tag("_dd.measured"), span.OperationName())
				assert.Nil(t, span.Tag("_dd.top_level"), span.OperationName())
			}
		}
	})
}

func TestProtocolTagsFor(t *testing.T) {
	connectTags := protocolTags{
		system:              "connectrpc",
		statusCodeTag:       "rpc.connect_rpc.error_code",
		requestTag:          "connect.request",
		statusDetailsPrefix: "connect.status_details.",
		notModifiedEligible: true,
	}
	grpcTags := protocolTags{
		system:              "grpc",
		grpcStatus:          true,
		statusCodeTag:       "rpc.grpc.status_code",
		requestTag:          "grpc.request",
		statusDetailsPrefix: "grpc.status_details.",
	}
	for _, test := range []struct {
		protocol string
		want     protocolTags
	}{
		{protocol: connectrpc.ProtocolConnect, want: connectTags},
		{protocol: connectrpc.ProtocolGRPC, want: grpcTags},
		{protocol: connectrpc.ProtocolGRPCWeb, want: grpcTags},
		{protocol: "", want: connectTags},
		{protocol: "future-protocol", want: connectTags},
	} {
		t.Run(fmt.Sprintf("%q", test.protocol), func(t *testing.T) {
			assert.Equal(t, test.want, *protocolTagsFor(test.protocol))
		})
	}
}

func TestProcedureTags(t *testing.T) {
	for _, test := range []struct {
		name string
		spec connectrpc.Spec
		want map[string]any
	}{
		{
			name: "unary", spec: connectrpc.Spec{Procedure: "/acme.v1.Svc/Get", StreamType: connectrpc.StreamTypeUnary},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Get", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Get", "connect.method.kind": "unary"},
		},
		{
			name: "client stream", spec: connectrpc.Spec{Procedure: "/acme.v1.Svc/Upload", StreamType: connectrpc.StreamTypeClient},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Upload", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Upload", "connect.method.kind": "client_streaming"},
		},
		{
			name: "server stream", spec: connectrpc.Spec{Procedure: "/acme.v1.Svc/Watch", StreamType: connectrpc.StreamTypeServer},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Watch", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Watch", "connect.method.kind": "server_streaming"},
		},
		{
			name: "bidi stream", spec: connectrpc.Spec{Procedure: "/acme.v1.Svc/Chat", StreamType: connectrpc.StreamTypeBidi},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Chat", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Chat", "connect.method.kind": "bidi_streaming"},
		},
		{
			name: "unknown stream type", spec: connectrpc.Spec{Procedure: "/acme.v1.Svc/Chat", StreamType: connectrpc.StreamType(255)},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Chat", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Chat", "connect.method.kind": "unknown"},
		},
		{
			name: "procedure without a method", spec: connectrpc.Spec{Procedure: "service"},
			want: map[string]any{ext.ResourceName: "service", ext.RPCService: "service", ext.RPCMethod: "", "connect.method.kind": "unary"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]any{}
			addProcedureTags(tags, test.spec)
			assert.Equal(t, test.want, tags)
		})
	}
}

func TestCodeOf(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want connectrpc.Code
	}{
		{name: "connect error", err: connectrpc.NewError(connectrpc.CodeNotFound, errors.New("nope")), want: connectrpc.CodeNotFound},
		{name: "explicit code beats a cancellation", err: connectrpc.NewError(connectrpc.CodeInternal, context.Canceled), want: connectrpc.CodeInternal},
		{name: "cancellation", err: fmt.Errorf("call: %w", context.Canceled), want: connectrpc.CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: connectrpc.CodeDeadlineExceeded},
		{name: "other error", err: errors.New("boom"), want: connectrpc.CodeUnknown},
		{name: "typed nil connect error", err: (*connectrpc.Error)(nil), want: connectrpc.CodeUnknown},
		{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}, want: connectrpc.CodeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, codeOf(test.err))
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
		{name: "connect end-of-stream sentinel", err: connectrpc.NewError(connectrpc.CodeUnknown, io.EOF), want: true},
		{name: "coded EOF", err: connectrpc.NewError(connectrpc.CodeInternal, io.EOF)},
		{name: "other error", err: errors.New("boom")},
		{name: "cancellation", err: context.Canceled},
		{name: "typed nil connect error", err: (*connectrpc.Error)(nil)},
		{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}},
		{name: "typed nil custom error", err: (*derefError)(nil)},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got bool
			require.NotPanics(t, func() { got = isExpectedStreamEOF(test.err) })
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
		{name: "typed nil connect error", err: (*connectrpc.Error)(nil)},
		{name: "typed nil custom error", err: (*derefError)(nil)},
		{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}},
		{name: "wrapped typed nil custom error", err: wrappedDerefError},
		{name: "wrapped typed nil error whose Is method panics", err: wrappedDerefIsError},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got error
			require.NotPanics(t, func() { got = normalizeError(test.err) })
			if test.wantSame {
				assert.Equal(t, test.err, got)
				return
			}
			require.Error(t, got)
			assert.True(t, isSafeToInspect(got))
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
		{name: "typed nil connect error", value: (*connectrpc.Error)(nil), wantMsg: "panic"},
		{name: "nil connect error in the chain", value: nilConnectErrorWrapper{}, wantMsg: "unsafe to inspect"},
		{name: "wrapped typed nil custom error", value: wrappedDerefError, wantMsg: "unsafe to inspect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = panicError(test.value) })
			require.Error(t, err)
			require.NotPanics(t, func() { _ = err.Error() })
			if test.check != nil {
				test.check(t, err)
			}
			assert.Contains(t, err.Error(), test.wantMsg)
		})
	}
}

func TestPeerTags(t *testing.T) {
	// The tracer stores the port as a float64 metric.
	for _, test := range []struct {
		addr string
		want map[string]any
	}{
		{addr: "127.0.0.1:8080", want: map[string]any{ext.NetworkDestinationIP: "127.0.0.1", ext.NetworkDestinationPort: 8080.0}},
		{addr: "[::1]:443", want: map[string]any{ext.NetworkDestinationIP: "::1", ext.NetworkDestinationPort: 443.0}},
		{addr: "::1", want: map[string]any{ext.NetworkDestinationIP: "::1"}},
		{addr: "example.com", want: map[string]any{ext.NetworkDestinationName: "example.com"}},
		{addr: "example.com:80", want: map[string]any{ext.NetworkDestinationName: "example.com", ext.NetworkDestinationPort: 80.0}},
		{addr: "example.com:http", want: map[string]any{ext.NetworkDestinationName: "example.com"}},
		{addr: "", want: map[string]any{}},
	} {
		t.Run(fmt.Sprintf("%q", test.addr), func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("peer")
			newPeerTags(connectrpc.Peer{Addr: test.addr}).set(span)
			span.Finish()
			got := map[string]any{}
			for _, key := range []string{ext.NetworkDestinationIP, ext.NetworkDestinationName, ext.NetworkDestinationPort} {
				if value := mt.FinishedSpans()[0].Tag(key); value != nil {
					got[key] = value
				}
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func TestInjectSpan(t *testing.T) {
	t.Run("without a span", func(t *testing.T) {
		header := make(http.Header)
		injectSpan(context.Background(), header)
		assert.Empty(t, header)
	})

	t.Run("with a span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		defer span.Finish()
		header := make(http.Header)
		injectSpan(ctx, header)
		assert.Equal(t, strconv.FormatUint(span.Context().SpanID(), 10), header.Get(tracer.DefaultParentIDHeader))
	})
}

func TestSetRequestTags(t *testing.T) {
	for _, test := range []struct {
		name     string
		opts     []Option
		protocol *protocolTags
		request  any
		want     map[string]any // a nil value means the tag is absent
	}{
		{name: "disabled", protocol: &connectProtocol, request: wrapperspb.String("hello"), want: map[string]any{"connect.request": nil}},
		{name: "connect", opts: []Option{WithRequestTags()}, protocol: &connectProtocol, request: wrapperspb.String("hello"), want: map[string]any{"connect.request": `"hello"`}},
		{name: "gRPC", opts: []Option{WithRequestTags()}, protocol: &grpcProtocol, request: wrapperspb.String("hello"), want: map[string]any{"grpc.request": `"hello"`, "connect.request": nil}},
		{name: "not a protobuf message", opts: []Option{WithRequestTags()}, protocol: &connectProtocol, request: "hello", want: map[string]any{"connect.request": nil}},
		{name: "unencodable message", opts: []Option{WithRequestTags()}, protocol: &connectProtocol, request: wrapperspb.String("\xff"), want: map[string]any{"connect.request": nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("request")
			setRequestTags(newConfig(test.opts...), test.request, test.protocol, span)
			span.Finish()
			for key, value := range test.want {
				assert.Equal(t, value, mt.FinishedSpans()[0].Tag(key), key)
			}
		})
	}

	t.Run("nil span", func(t *testing.T) {
		assert.NotPanics(t, func() {
			setRequestTags(newConfig(WithRequestTags()), wrapperspb.String("hello"), &connectProtocol, nil)
		})
	})
}

func TestSetErrorDetailTags(t *testing.T) {
	undecodable, err := connectrpc.NewErrorDetail(&anypb.Any{TypeUrl: "type.googleapis.com/does.not.Exist", Value: []byte{1}})
	require.NoError(t, err)
	withUndecodable := connectrpc.NewError(connectrpc.CodeInternal, errors.New("boom"))
	withUndecodable.AddDetail(undecodable)
	withUndecodable.AddDetail(errorWithDetails(connectrpc.CodeInternal).Details()[0])
	for _, test := range []struct {
		name     string
		err      error
		protocol *protocolTags
		want     map[string]any
	}{
		{name: "connect", err: errorWithDetails(connectrpc.CodeInternal), protocol: &connectProtocol, want: map[string]any{"connect.status_details._0": "seconds:1"}},
		{name: "gRPC", err: errorWithDetails(connectrpc.CodeInternal), protocol: &grpcProtocol, want: map[string]any{"grpc.status_details._0": "seconds:1"}},
		{name: "wrapped", err: fmt.Errorf("call: %w", errorWithDetails(connectrpc.CodeInternal)), protocol: &connectProtocol, want: map[string]any{"connect.status_details._0": "seconds:1"}},
		{name: "undecodable detail is skipped", err: withUndecodable, protocol: &connectProtocol, want: map[string]any{"connect.status_details._1": "seconds:1"}},
		{name: "not a connect error", err: errors.New("boom"), protocol: &connectProtocol, want: map[string]any{}},
		{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}, protocol: &connectProtocol, want: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("details")
			require.NotPanics(t, func() { setErrorDetailTags(span, test.err, test.protocol) })
			span.Finish()
			got := tagsWithPrefix(mt.FinishedSpans()[0], "connect.status_details.")
			maps.Copy(got, tagsWithPrefix(mt.FinishedSpans()[0], "grpc.status_details."))
			assert.Equal(t, test.want, got)
		})
	}
}
