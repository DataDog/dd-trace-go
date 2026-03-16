// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package namingschematest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc/connect-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type connectTestReq struct {
	Name string `json:"name"`
}

type connectTestResp struct {
	Message string `json:"message"`
}

var (
	connectRPCServerTest = harness.TestCase{
		Name:     instrumentation.PackageConnectRPC + "_server",
		GenSpans: connectRPCGenSpansFn(false, true),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        harness.RepeatString("connect.server", 2),
			DDService:       harness.RepeatString(harness.TestDDService, 2),
			ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			for i := 0; i < 2; i++ {
				assert.Equal(t, "connect.server", spans[i].OperationName())
			}
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			for i := 0; i < 2; i++ {
				assert.Equal(t, "connect.server.request", spans[i].OperationName())
			}
		},
	}
	connectRPCClientTest = harness.TestCase{
		Name:     instrumentation.PackageConnectRPC + "_client",
		GenSpans: connectRPCGenSpansFn(true, false),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        harness.RepeatString("connect.client", 2),
			DDService:       harness.RepeatString("connect.client", 2),
			ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			for i := 0; i < 2; i++ {
				assert.Equal(t, "connect.client", spans[i].OperationName())
			}
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			for i := 0; i < 2; i++ {
				assert.Equal(t, "connect.client.request", spans[i].OperationName())
			}
		},
	}
)

func connectRPCGenSpansFn(traceClient, traceServer bool) harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var serverOpts []connecttrace.Option
		var clientOpts []connecttrace.Option
		if serviceOverride != "" {
			serverOpts = append(serverOpts, connecttrace.WithService(serviceOverride))
			clientOpts = append(clientOpts, connecttrace.WithService(serviceOverride))
		}
		// Exclude streaming message spans as they are not affected by naming schema
		serverOpts = append(serverOpts, connecttrace.WithStreamMessages(false))
		clientOpts = append(clientOpts, connecttrace.WithStreamMessages(false))

		mt := mocktracer.Start()
		defer mt.Stop()

		// Build handler/server
		var serverInterceptors []connect.HandlerOption
		if traceServer {
			serverInterceptors = append(serverInterceptors, connect.WithInterceptors(connecttrace.NewInterceptor(serverOpts...)))
		}

		mux := http.NewServeMux()
		// Unary handler
		mux.Handle("/test.v1.TestService/Ping", connect.NewUnaryHandler(
			"/test.v1.TestService/Ping",
			func(_ context.Context, req *connect.Request[connectTestReq]) (*connect.Response[connectTestResp], error) {
				return connect.NewResponse(&connectTestResp{Message: "pong"}), nil
			},
			serverInterceptors...,
		))
		// Server stream handler
		mux.Handle("/test.v1.TestService/ServerStream", connect.NewServerStreamHandler(
			"/test.v1.TestService/ServerStream",
			func(_ context.Context, _ *connect.Request[connectTestReq], stream *connect.ServerStream[connectTestResp]) error {
				return stream.Send(&connectTestResp{Message: "msg"})
			},
			serverInterceptors...,
		))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		// Build client interceptors
		var clientInterceptorOpts []connect.ClientOption
		if traceClient {
			clientInterceptorOpts = append(clientInterceptorOpts, connect.WithInterceptors(connecttrace.NewInterceptor(clientOpts...)))
		}

		// Call unary
		unaryClient := connect.NewClient[connectTestReq, connectTestResp](
			http.DefaultClient,
			srv.URL+"/test.v1.TestService/Ping",
			clientInterceptorOpts...,
		)
		_, err := unaryClient.CallUnary(context.Background(), connect.NewRequest(&connectTestReq{Name: "test"}))
		require.NoError(t, err)

		// Call server stream
		streamClient := connect.NewClient[connectTestReq, connectTestResp](
			http.DefaultClient,
			srv.URL+"/test.v1.TestService/ServerStream",
			clientInterceptorOpts...,
		)
		stream, err := streamClient.CallServerStream(context.Background(), connect.NewRequest(&connectTestReq{Name: "test"}))
		require.NoError(t, err)
		for stream.Receive() {
			_ = stream.Msg()
		}
		require.NoError(t, stream.Err())
		require.NoError(t, stream.Close())

		return mt.FinishedSpans()
	}
}
