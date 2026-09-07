// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package namingschematest

import (
	"context"
	"net/http/httptest"
	"testing"

	connectrpc "connectrpc.com/connect"
	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

const connectProcedure = "/test.connect.v1.TestService/Ping"

var (
	connectServerTest = harness.TestCase{
		Name:     instrumentation.PackageConnectRPC + "_server",
		GenSpans: connectGenSpans(false, true),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"connect.server"},
			DDService:       []string{harness.TestDDService},
			ServiceOverride: []string{harness.TestServiceOverride},
		},
		WantServiceSource: harness.ServiceSourceAssertions{
			Defaults:        []string{string(instrumentation.PackageConnectRPC)},
			ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "connect.server", spans[0].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "connect.server.request", spans[0].OperationName())
		},
	}
	connectClientTest = harness.TestCase{
		Name:     instrumentation.PackageConnectRPC + "_client",
		GenSpans: connectGenSpans(true, false),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"connect.client"},
			DDService:       []string{"connect.client"},
			ServiceOverride: []string{harness.TestServiceOverride},
		},
		WantServiceSource: harness.ServiceSourceAssertions{
			Defaults:        []string{string(instrumentation.PackageConnectRPC)},
			ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "connect.client", spans[0].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "connect.client.request", spans[0].OperationName())
		},
	}
)

func connectGenSpans(traceClient, traceServer bool) harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []connecttrace.Option
		if serviceOverride != "" {
			opts = append(opts, connecttrace.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()
		var handlerOpts []connectrpc.HandlerOption
		if traceServer {
			handlerOpts = append(handlerOpts, connectrpc.WithInterceptors(connecttrace.NewServerInterceptor(opts...)))
		}
		handler := connectrpc.NewUnaryHandler(
			connectProcedure,
			func(context.Context, *connectrpc.Request[emptypb.Empty]) (*connectrpc.Response[emptypb.Empty], error) {
				return connectrpc.NewResponse(&emptypb.Empty{}), nil
			},
			handlerOpts...,
		)
		server := httptest.NewServer(handler)
		defer server.Close()

		var clientOpts []connectrpc.ClientOption
		if traceClient {
			clientOpts = append(clientOpts, connectrpc.WithInterceptors(connecttrace.NewClientInterceptor(opts...)))
		}
		client := connectrpc.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+connectProcedure, clientOpts...)
		_, err := client.CallUnary(context.Background(), connectrpc.NewRequest(&emptypb.Empty{}))
		require.NoError(t, err)
		require.Len(t, mt.FinishedSpans(), 1)
		return mt.FinishedSpans()
	}
}
