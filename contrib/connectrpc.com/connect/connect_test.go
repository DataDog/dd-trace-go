// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	unaryProcedure        = "/test.connect.v1.TestService/Unary"
	clientStreamProcedure = "/test.connect.v1.TestService/ClientStream"
	serverStreamProcedure = "/test.connect.v1.TestService/ServerStream"
	bidiProcedure         = "/test.connect.v1.TestService/Bidi"
)

type testRig struct {
	server       *httptest.Server
	clientOpts   []Option
	protocolOpts []connectrpc.ClientOption
}

func newTestRig(t *testing.T, serverOpts, clientOpts []Option, protocolOpts ...connectrpc.ClientOption) *testRig {
	t.Helper()
	serverInterceptor := NewServerInterceptor(serverOpts...)
	handlerOpts := []connectrpc.HandlerOption{connectrpc.WithInterceptors(serverInterceptor)}
	mux := http.NewServeMux()
	mux.Handle(unaryProcedure, connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler, handlerOpts...))
	mux.Handle(clientStreamProcedure, connectrpc.NewClientStreamHandler(clientStreamProcedure, clientStreamHandler, handlerOpts...))
	mux.Handle(serverStreamProcedure, connectrpc.NewServerStreamHandler(serverStreamProcedure, serverStreamHandler, handlerOpts...))
	mux.Handle(bidiProcedure, connectrpc.NewBidiStreamHandler(bidiProcedure, bidiHandler, handlerOpts...))

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return &testRig{server: server, clientOpts: clientOpts, protocolOpts: protocolOpts}
}

func (r *testRig) client(procedure string) *connectrpc.Client[wrapperspb.StringValue, wrapperspb.StringValue] {
	opts := make([]connectrpc.ClientOption, 1, 1+len(r.protocolOpts))
	opts[0] = connectrpc.WithInterceptors(NewClientInterceptor(r.clientOpts...))
	opts = append(opts, r.protocolOpts...)
	return connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		r.server.Client(),
		r.server.URL+procedure,
		opts...,
	)
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

func serverStreamHandler(_ context.Context, request *connectrpc.Request[wrapperspb.StringValue], stream *connectrpc.ServerStream[wrapperspb.StringValue]) error {
	if err := testError(request.Msg.Value); err != nil {
		return err
	}
	for _, value := range []string{request.Msg.Value + "-1", request.Msg.Value + "-2"} {
		if err := stream.Send(wrapperspb.String(value)); err != nil {
			return err
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

func testError(value string) error {
	switch value {
	case "not-found":
		return connectrpc.NewError(connectrpc.CodeNotFound, errors.New("not found"))
	case "error-details":
		err := connectrpc.NewError(connectrpc.CodeInternal, errors.New("internal error"))
		detail, detailErr := connectrpc.NewErrorDetail(&durationpb.Duration{Seconds: 1})
		if detailErr != nil {
			panic(detailErr)
		}
		err.AddDetail(detail)
		return err
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

func TestUnaryProtocols(t *testing.T) {
	tests := []struct {
		name         string
		protocolOpts []connectrpc.ClientOption
		wantSystem   string
	}{
		{name: "connect", wantSystem: ext.RPCSystemConnectRPC},
		{name: "grpc", protocolOpts: []connectrpc.ClientOption{connectrpc.WithGRPC()}, wantSystem: ext.RPCSystemGRPC},
		{name: "grpc-web", protocolOpts: []connectrpc.ClientOption{connectrpc.WithGRPCWeb()}, wantSystem: ext.RPCSystemGRPC},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			opts := []Option{
				WithService("connect-test"),
				WithHeaderTags(),
				WithIgnoredHeaders("X-Ignored"),
				WithRequestTags(),
				WithCustomTag("custom-tag", "custom-value"),
				WithSpanOptions(tracer.Tag("span-option", "span-value")),
			}
			rig := newTestRig(t, opts, opts, test.protocolOpts...)
			request := connectrpc.NewRequest(wrapperspb.String("hello"))
			request.Header().Set("X-Test", "visible")
			request.Header().Set("X-Ignored", "hidden")
			request.Header().Set("Authorization", "Bearer secret")
			request.Header().Set("B3", "trace-span-1")
			request.Header().Set("Ot-Baggage-Secret", "secret")
			request.Header().Set("Payload-Bin", "binary")
			request.Header().Set("X-B3-Traceid", "trace")
			response, err := rig.client(unaryProcedure).CallUnary(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, "hello", response.Msg.Value)

			clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
			for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
				assert.Equal(t, test.wantSystem, span.Tag(ext.RPCSystem))
				assert.Equal(t, "test.connect.v1.TestService", span.Tag(ext.RPCService))
				assert.Equal(t, "Unary", span.Tag(ext.RPCMethod))
				assert.Equal(t, unaryProcedure, span.Tag(ext.ResourceName))
				assert.Equal(t, ext.AppTypeRPC, span.Tag(ext.SpanType))
				assert.Equal(t, "connect-test", span.Tag(ext.ServiceName))
				assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, span.Tag(ext.KeyServiceSource))
				assert.Equal(t, "custom-value", span.Tag("custom-tag"))
				assert.Equal(t, "span-value", span.Tag("span-option"))
			}
			assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
			assert.Equal(t, clientSpan.TraceID(), serverSpan.TraceID())
			assert.NotNil(t, metadataTag(clientSpan, test.wantSystem, "x-test"))
			assert.NotNil(t, metadataTag(serverSpan, test.wantSystem, "x-test"))
			for _, key := range []string{"authorization", "b3", "ot-baggage-secret", "x-b3-traceid", "x-ignored", "payload-bin", "x-datadog-trace-id"} {
				assert.Nil(t, metadataTag(clientSpan, test.wantSystem, key))
				assert.Nil(t, metadataTag(serverSpan, test.wantSystem, key))
			}
			requestTag := tagConnectRequest
			if test.wantSystem == ext.RPCSystemGRPC {
				requestTag = tagGRPCRequest
				assert.EqualValues(t, 0, clientSpan.Tag(tagGRPCStatusCode))
				assert.EqualValues(t, 0, serverSpan.Tag(tagGRPCStatusCode))
			}
			assert.Equal(t, `"hello"`, clientSpan.Tag(requestTag))
			assert.Equal(t, `"hello"`, serverSpan.Tag(requestTag))
			assert.Equal(t, "127.0.0.1", clientSpan.Tag(ext.NetworkDestinationIP))
			port, ok := clientSpan.Tag(ext.NetworkDestinationPort).(float64)
			assert.True(t, ok)
			assert.Positive(t, port)
		})
	}
}

func TestUnaryErrorsAndOptions(t *testing.T) {
	t.Run("non-error code retains status", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{NonErrorCodes(connectrpc.CodeNotFound)}
		rig := newTestRig(t, opts, opts)
		_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("not-found")))
		require.Error(t, err)
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			assert.Equal(t, "not_found", span.Tag(tagConnectErrorCode))
			assert.Nil(t, span.Tag(ext.ErrorMsg))
		}
	})

	t.Run("error details and analytics", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithErrorDetailTags(), WithAnalyticsRate(0.5), NoDebugStack()}
		rig := newTestRig(t, opts, opts)
		_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("error-details")))
		require.Error(t, err)
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			assert.Equal(t, "internal", span.Tag(tagConnectErrorCode))
			assert.NotNil(t, span.Tag(ext.ErrorMsg))
			assert.Equal(t, 0.5, span.Tag(ext.EventSampleRate))
			assert.Equal(t, "seconds:1", span.Tag("connect.status_details._0"))
		}
	})

	for _, test := range []struct {
		name     string
		value    string
		wantCode string
	}{
		{name: "explicit code takes precedence", value: "coded-canceled", wantCode: "internal"},
		{name: "unary EOF is an error", value: "eof", wantCode: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			rig := newTestRig(t, nil, nil)
			_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String(test.value)))
			require.Error(t, err)
			clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
			for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
				assert.Equal(t, test.wantCode, span.Tag(tagConnectErrorCode))
				assert.NotNilf(t, span.Tag(ext.ErrorMsg), "span.kind=%v tags=%v", span.Tag(ext.SpanKind), span.Tags())
			}
		})
	}

	t.Run("error check overrides error status", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithErrorCheck(func(procedure string, err error) bool {
			return procedure != unaryProcedure
		})}
		rig := newTestRig(t, opts, opts)
		_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("error")))
		require.Error(t, err)
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindUnary)
		for _, span := range []*mocktracer.Span{clientSpan, serverSpan} {
			assert.Equal(t, "internal", span.Tag(tagConnectErrorCode))
			assert.Nil(t, span.Tag(ext.ErrorMsg))
		}
	})

	t.Run("untraced", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithUntracedMethods(unaryProcedure)}
		rig := newTestRig(t, opts, opts)
		_, err := rig.client(unaryProcedure).CallUnary(context.Background(), connectrpc.NewRequest(wrapperspb.String("hello")))
		require.NoError(t, err)
		assert.Empty(t, mt.FinishedSpans())
	})
}

func TestStreamingCalls(t *testing.T) {
	streamTests := []struct {
		name      string
		procedure string
		kind      string
		call      func(*testing.T, *testRig)
	}{
		{
			name: "client", procedure: clientStreamProcedure, kind: methodKindClientStream,
			call: func(t *testing.T, rig *testRig) {
				stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				response, err := stream.CloseAndReceive()
				require.NoError(t, err)
				assert.Equal(t, "hello", response.Msg.Value)
			},
		},
		{
			name: "server", procedure: serverStreamProcedure, kind: methodKindServerStream,
			call: func(t *testing.T, rig *testRig) {
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
			name: "bidi", procedure: bidiProcedure, kind: methodKindBidiStream,
			call: func(t *testing.T, rig *testRig) {
				stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
				require.NoError(t, stream.Send(wrapperspb.String("hello")))
				response, err := stream.Receive()
				require.NoError(t, err)
				assert.Equal(t, "hello", response.Value)
				require.NoError(t, stream.CloseRequest())
				_, err = stream.Receive()
				assert.ErrorIs(t, err, io.EOF)
				require.NoError(t, stream.CloseResponse())
			},
		},
	}
	protocolTests := []struct {
		name          string
		opts          []connectrpc.ClientOption
		wantRPCSystem string
	}{
		{name: "connect", wantRPCSystem: ext.RPCSystemConnectRPC},
		{name: "grpc", opts: []connectrpc.ClientOption{connectrpc.WithGRPC()}, wantRPCSystem: ext.RPCSystemGRPC},
		{name: "grpc-web", opts: []connectrpc.ClientOption{connectrpc.WithGRPCWeb()}, wantRPCSystem: ext.RPCSystemGRPC},
	}
	for _, streamTest := range streamTests {
		for _, protocolTest := range protocolTests {
			t.Run(streamTest.name+"/"+protocolTest.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				opts := []Option{WithStreamMessages(false)}
				rig := newTestRig(t, opts, opts, protocolTest.opts...)
				streamTest.call(t, rig)
				clientSpan, serverSpan := requireCallPair(t, mt, streamTest.kind)
				assert.Equal(t, streamTest.procedure, clientSpan.Tag(ext.ResourceName))
				assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
				assert.Equal(t, protocolTest.wantRPCSystem, clientSpan.Tag(ext.RPCSystem))
				assert.Equal(t, protocolTest.wantRPCSystem, serverSpan.Tag(ext.RPCSystem))
			})
		}
	}
}

func TestStreamingSpanLifecycle(t *testing.T) {
	t.Run("open until completion and retains terminal error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithStreamMessages(false)}
		rig := newTestRig(t, opts, opts)
		stream := rig.client(clientStreamProcedure).CallClientStream(context.Background())
		require.NoError(t, stream.Send(wrapperspb.String("error")))
		assert.Empty(t, callSpans(mt.FinishedSpans(), ext.SpanKindClient), "client span finished before stream completion")
		_, err := stream.CloseAndReceive()
		require.Error(t, err)
		clientSpan, _ := requireCallPair(t, mt, methodKindClientStream)
		assert.Equal(t, "internal", clientSpan.Tag(tagConnectErrorCode))
		assert.NotNil(t, clientSpan.Tag(ext.ErrorMsg))
	})

	t.Run("context cancellation", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithStreamMessages(false)}
		rig := newTestRig(t, opts, opts)
		ctx, cancel := context.WithCancel(context.Background())
		_ = rig.client(bidiProcedure).CallBidiStream(ctx)
		cancel()
		require.Eventually(t, func() bool {
			return len(callSpans(mt.FinishedSpans(), ext.SpanKindClient)) == 1
		}, time.Second, 10*time.Millisecond)
		span := callSpans(mt.FinishedSpans(), ext.SpanKindClient)[0]
		assert.Equal(t, "canceled", span.Tag(tagConnectErrorCode))
		assert.Nil(t, span.Tag(ext.ErrorMsg))
	})

	t.Run("captures headers set after stream construction", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		opts := []Option{WithStreamMessages(false), WithHeaderTags()}
		rig := newTestRig(t, opts, opts)
		stream := rig.client(bidiProcedure).CallBidiStream(context.Background())
		stream.RequestHeader().Set("X-Late-Header", "value")
		require.NoError(t, stream.Send(wrapperspb.String("hello")))
		_, err := stream.Receive()
		require.NoError(t, err)
		require.NoError(t, stream.CloseRequest())
		_, err = stream.Receive()
		assert.ErrorIs(t, err, io.EOF)
		require.NoError(t, stream.CloseResponse())
		clientSpan, serverSpan := requireCallPair(t, mt, methodKindBidiStream)
		assert.NotNil(t, metadataTag(clientSpan, ext.RPCSystemConnectRPC, "x-late-header"))
		assert.NotNil(t, metadataTag(serverSpan, ext.RPCSystemConnectRPC, "x-late-header"))
	})

	t.Run("coded EOF remains a call error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		interceptor := NewClientInterceptor(WithStreamMessages(false))
		wrapped := interceptor.WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
			return &terminalErrorStreamingClientConn{
				panicStreamingClientConn: panicStreamingClientConn{header: make(http.Header)},
				err:                      connectrpc.NewError(connectrpc.CodeInternal, io.EOF),
			}
		})(context.Background(), connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true})
		err := wrapped.Receive(nil)
		require.ErrorIs(t, err, io.EOF)
		require.Len(t, mt.FinishedSpans(), 1)
		assert.Equal(t, "internal", mt.FinishedSpans()[0].Tag(tagConnectErrorCode))
		assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
	})
}

func TestStreamingMessagesWithoutCallSpans(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	opts := []Option{WithStreamCalls(false), WithRequestTags(), WithHeaderTags()}
	rig := newTestRig(t, opts, opts)
	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	stream := rig.client(bidiProcedure).CallBidiStream(ctx)
	stream.RequestHeader().Set("X-Message-Header", "value")
	require.NoError(t, stream.Send(wrapperspb.String("hello")))
	response, err := stream.Receive()
	require.NoError(t, err)
	assert.Equal(t, "hello", response.Value)
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	assert.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.CloseResponse())
	parent.Finish()

	spans := mt.FinishedSpans()
	assert.Empty(t, callSpans(spans, ext.SpanKindClient))
	assert.Empty(t, callSpans(spans, ext.SpanKindServer))
	messageCount := 0
	clientHeaderFound := false
	serverHeaderFound := false
	clientPeerFound := false
	for _, span := range spans {
		if span.OperationName() != "connect.message" {
			continue
		}
		messageCount++
		assert.Equal(t, parent.Context().TraceIDLower(), span.TraceID())
		assert.Equal(t, parent.Context().SpanID(), span.ParentID())
		if metadataTag(span, ext.RPCSystemConnectRPC, "x-message-header") != nil {
			switch span.Tag(ext.ServiceName) {
			case "connect.client":
				clientHeaderFound = true
			case "connect.server":
				serverHeaderFound = true
			}
		}
		if span.Tag(ext.ServiceName) == "connect.client" && span.Tag(ext.NetworkDestinationIP) == "127.0.0.1" {
			clientPeerFound = true
		}
	}
	assert.GreaterOrEqual(t, messageCount, 4)
	assert.True(t, clientHeaderFound)
	assert.True(t, serverHeaderFound)
	assert.True(t, clientPeerFound)
}

func requireCallPair(t *testing.T, mt mocktracer.Tracer, kind string) (client, server *mocktracer.Span) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(callSpans(mt.FinishedSpans(), ext.SpanKindClient)) == 1 && len(callSpans(mt.FinishedSpans(), ext.SpanKindServer)) == 1
	}, time.Second, 10*time.Millisecond)
	client = callSpans(mt.FinishedSpans(), ext.SpanKindClient)[0]
	server = callSpans(mt.FinishedSpans(), ext.SpanKindServer)[0]
	assert.Equal(t, kind, client.Tag(tagMethodKind))
	assert.Equal(t, kind, server.Tag(tagMethodKind))
	return client, server
}

func callSpans(spans []*mocktracer.Span, spanKind string) []*mocktracer.Span {
	var calls []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.SpanKind) == spanKind {
			calls = append(calls, span)
		}
	}
	return calls
}

func metadataPrefix(system string) string {
	if system == ext.RPCSystemGRPC {
		return "rpc.grpc.request.metadata."
	}
	return "rpc.connect_rpc.request.metadata."
}

func metadataTag(span *mocktracer.Span, system, key string) any {
	prefix := metadataPrefix(system) + key
	if value := span.Tag(prefix); value != nil {
		return value
	}
	return span.Tag(prefix + ".0")
}

func TestHelpers(t *testing.T) {
	service, method := parseProcedure("service")
	assert.Equal(t, "service", service)
	assert.Empty(t, method)
	assert.Equal(t, "unknown", methodKind(connectrpc.StreamType(255)))
	assert.Equal(t, ext.RPCSystemConnectRPC, rpcSystem("future-protocol"))
	assert.True(t, strings.HasPrefix(metadataPrefix(ext.RPCSystemConnectRPC), "rpc.connect_rpc"))
	assert.Equal(t, connectrpc.CodeDeadlineExceeded, codeOf(context.DeadlineExceeded))
	assert.Equal(t, connectrpc.CodeInternal, codeOf(connectrpc.NewError(connectrpc.CodeInternal, context.Canceled)))
	require.NotNil(t, NewInterceptor())
	assert.Equal(t, 1.0, newConfig(WithAnalytics(true)).analyticsRate)
	assert.True(t, math.IsNaN(newConfig(WithAnalytics(false)).analyticsRate))
	testErr := errors.New("panic error")
	assert.ErrorIs(t, panicError(testErr), testErr)

	header := make(http.Header)
	injectSpan(context.Background(), header)
	assert.Empty(t, header)
	mt := mocktracer.Start()
	span := tracer.StartSpan("peer")
	setPeerTags(span, connectrpc.Peer{Addr: "example.com"})
	span.Finish()
	mt.Stop()
	require.Len(t, mt.FinishedSpans(), 1)
	assert.Equal(t, "example.com", mt.FinishedSpans()[0].Tag(ext.NetworkDestinationName))
	assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.NetworkDestinationPort))
}

func TestNotModified(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		httpMethod string
		want304    bool
	}{
		{name: "connect get", protocol: connectrpc.ProtocolConnect, httpMethod: http.MethodGet, want304: true},
		{name: "connect post", protocol: connectrpc.ProtocolConnect, httpMethod: http.MethodPost},
		{name: "grpc", protocol: connectrpc.ProtocolGRPC, httpMethod: http.MethodGet},
		{name: "grpc-web", protocol: connectrpc.ProtocolGRPCWeb, httpMethod: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("not-modified")
			finishUnary(span, connectrpc.NewNotModifiedError(nil), unaryProcedure, test.protocol, test.httpMethod, newConfig())
			require.Len(t, mt.FinishedSpans(), 1)
			if test.want304 {
				assert.EqualValues(t, 304, mt.FinishedSpans()[0].Tag(ext.HTTPCode))
				assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
				return
			}
			assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.HTTPCode))
			assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
		})
	}
}

func TestFinishMessageEOF(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  any
		wantError bool
	}{
		{name: "normal EOF", err: io.EOF},
		{name: "coded EOF", err: connectrpc.NewError(connectrpc.CodeInternal, io.EOF), wantCode: "internal", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("message")
			finishMessage(span, test.err, unaryProcedure, connectrpc.ProtocolConnect, newConfig())
			require.Len(t, mt.FinishedSpans(), 1)
			assert.Equal(t, test.wantCode, mt.FinishedSpans()[0].Tag(tagConnectErrorCode))
			if test.wantError {
				assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
			}
		})
	}
}

type panicStreamingClientConn struct {
	header http.Header
}

func (c *panicStreamingClientConn) Spec() connectrpc.Spec {
	return connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true}
}

func (c *panicStreamingClientConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "localhost:8080", Protocol: connectrpc.ProtocolConnect}
}

func (c *panicStreamingClientConn) Send(any) error             { panic("send panic") }
func (c *panicStreamingClientConn) RequestHeader() http.Header { return c.header }
func (c *panicStreamingClientConn) CloseRequest() error        { return nil }
func (c *panicStreamingClientConn) Receive(any) error          { return io.EOF }
func (c *panicStreamingClientConn) ResponseHeader() http.Header {
	return make(http.Header)
}
func (c *panicStreamingClientConn) ResponseTrailer() http.Header {
	return make(http.Header)
}
func (c *panicStreamingClientConn) CloseResponse() error { return nil }

type panicStreamingHandlerConn struct{}

type terminalErrorStreamingClientConn struct {
	panicStreamingClientConn
	err error
}

func (c *terminalErrorStreamingClientConn) Send(any) error    { return nil }
func (c *terminalErrorStreamingClientConn) Receive(any) error { return c.err }

func (panicStreamingHandlerConn) Spec() connectrpc.Spec {
	return connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi}
}

func (panicStreamingHandlerConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Protocol: connectrpc.ProtocolConnect}
}

func (panicStreamingHandlerConn) Receive(any) error            { panic("receive panic") }
func (panicStreamingHandlerConn) RequestHeader() http.Header   { return make(http.Header) }
func (panicStreamingHandlerConn) Send(any) error               { return nil }
func (panicStreamingHandlerConn) ResponseHeader() http.Header  { return make(http.Header) }
func (panicStreamingHandlerConn) ResponseTrailer() http.Header { return make(http.Header) }

func TestStreamingPanicsFinishSpans(t *testing.T) {
	t.Run("client constructor", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		interceptor := NewClientInterceptor()
		wrapped := interceptor.WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
			panic("constructor panic")
		})
		assert.Panics(t, func() {
			_ = wrapped(context.Background(), connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true})
		})
		require.Len(t, mt.FinishedSpans(), 1)
		assert.NotNil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
		assert.Equal(t, ext.RPCSystemConnectRPC, mt.FinishedSpans()[0].Tag(ext.RPCSystem), "call span must carry rpc.system even when next() panics before a peer is known")
	})

	t.Run("client", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		interceptor := NewClientInterceptor()
		wrapped := interceptor.WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
			return &panicStreamingClientConn{header: make(http.Header)}
		})(context.Background(), connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true})
		assert.Panics(t, func() { _ = wrapped.Send(wrapperspb.String("hello")) })
		require.Len(t, mt.FinishedSpans(), 2)
		for _, span := range mt.FinishedSpans() {
			assert.NotNil(t, span.Tag(ext.ErrorMsg))
		}
	})

	t.Run("server", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		interceptor := NewServerInterceptor()
		wrapped := interceptor.WrapStreamingHandler(func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
			return conn.Receive(nil)
		})
		assert.Panics(t, func() { _ = wrapped(context.Background(), panicStreamingHandlerConn{}) })
		require.Len(t, mt.FinishedSpans(), 2)
		for _, span := range mt.FinishedSpans() {
			assert.NotNil(t, span.Tag(ext.ErrorMsg))
		}
	})
}

func TestStreamingClientTerminalErrorPrefersRealError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	span := tracer.StartSpan("connect.client")
	conn := &streamingClientConn{
		StreamingClientConn: &panicStreamingClientConn{header: make(http.Header)},
		cfg:                 newConfig(),
		ctx:                 context.Background(),
		span:                span,
	}

	conn.beginOperation()
	// The context is observed as canceled first (e.g. via context.AfterFunc)...
	conn.requestFinish(context.Canceled)
	// ...but the in-flight Send/Receive concurrently completes with the real cause.
	// The real error must win over the generic cancellation.
	realErr := connectrpc.NewError(connectrpc.CodeUnavailable, errors.New("boom"))
	conn.endOperation(realErr, true)

	require.Len(t, mt.FinishedSpans(), 1)
	finished := mt.FinishedSpans()[0]
	assert.Equal(t, "unavailable", finished.Tag(tagConnectErrorCode))
	require.NotNil(t, finished.Tag(ext.ErrorMsg))
	assert.Contains(t, finished.Tag(ext.ErrorMsg), "boom")
}
