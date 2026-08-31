// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const (
	unaryProcedure  = "/example.echo.v1.EchoService/Echo"
	streamProcedure = "/example.echo.v1.EchoService/Collect"
)

type TestCase struct {
	unaryClient       *connect.Client[wrapperspb.StringValue, wrapperspb.StringValue]
	streamClient      *connect.Client[wrapperspb.StringValue, wrapperspb.StringValue]
	clientIntercepted atomic.Bool
	serverIntercepted atomic.Bool
}

func (tc *TestCase) Setup(_ context.Context, _ *testing.T) {
	unaryHandler := connect.NewUnaryHandlerSimple(
		unaryProcedure,
		func(_ context.Context, request *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return wrapperspb.String(request.Value), nil
		},
		connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				tc.serverIntercepted.Store(true)
				return next(ctx, request)
			}
		})),
	)
	streamHandler := connect.NewClientStreamHandlerSimple(
		streamProcedure,
		func(_ context.Context, stream *connect.ClientStream[wrapperspb.StringValue]) (*wrapperspb.StringValue, error) {
			var result strings.Builder
			for stream.Receive() {
				result.WriteString(stream.Msg().Value)
			}
			if err := stream.Err(); err != nil {
				return nil, err
			}
			return wrapperspb.String(result.String()), nil
		},
	)
	httpClient := handlerHTTPClient{handlers: map[string]http.Handler{
		unaryProcedure:  unaryHandler,
		streamProcedure: streamHandler,
	}}
	tc.unaryClient = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		httpClient,
		"http://connect.example"+unaryProcedure,
		connect.WithClientOptions(connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				tc.clientIntercepted.Store(true)
				return next(ctx, request)
			}
		}))),
	)
	tc.streamClient = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		httpClient,
		"http://connect.example"+streamProcedure,
		connect.WithClientOptions(),
	)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	response, err := tc.unaryClient.CallUnary(ctx, connect.NewRequest(wrapperspb.String("hello")))
	require.NoError(t, err)
	require.Equal(t, "hello", response.Msg.Value)

	streamCtx, callInfo := connect.NewClientContext(ctx)
	callInfo.RequestHeader().Set("X-Test-Header", "value")
	stream, err := tc.streamClient.CallClientStreamSimple(streamCtx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(wrapperspb.String("hello")))
	require.NoError(t, stream.Send(wrapperspb.String(" world")))
	streamResponse, err := stream.CloseAndReceive()
	require.NoError(t, err)
	require.Equal(t, "hello world", streamResponse.Value)
	require.True(t, tc.clientIntercepted.Load())
	require.True(t, tc.serverIntercepted.Load())
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		expectedTrace(unaryProcedure, "Echo", "unary"),
		expectedTrace(streamProcedure, "Collect", "client_streaming"),
	}
}

func expectedTrace(procedure, method, methodKind string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "connect.client",
			"service":  "connect.client",
			"resource": procedure,
			"type":     "rpc",
		},
		Meta: map[string]string{
			"component":       "connectrpc.com/connect",
			"span.kind":       "client",
			"rpc.system":      "connect_rpc",
			"rpc.service":     "example.echo.v1.EchoService",
			"rpc.method":      method,
			"rpc.method.kind": methodKind,
		},
		Children: trace.Traces{
			{
				Tags: map[string]any{
					"name":     "connect.server",
					"service":  "connect.server",
					"resource": procedure,
					"type":     "rpc",
				},
				Meta: map[string]string{
					"component":       "connectrpc.com/connect",
					"span.kind":       "server",
					"rpc.system":      "connect_rpc",
					"rpc.service":     "example.echo.v1.EchoService",
					"rpc.method":      method,
					"rpc.method.kind": methodKind,
				},
			},
		},
	}
}

type handlerHTTPClient struct {
	handlers map[string]http.Handler
}

func (c handlerHTTPClient) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	c.handlers[request.URL.Path].ServeHTTP(recorder, request)
	return recorder.Result(), nil
}
