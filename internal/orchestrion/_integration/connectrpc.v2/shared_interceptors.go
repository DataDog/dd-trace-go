// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSharedInterceptors builds two servers, and two clients, from interceptor slices that
// share a backing array with spare capacity. The woven code must not write into that array:
// building the first server or client would otherwise overwrite the interceptor that the second
// one adds.
type TestCaseSharedInterceptors struct {
	baseServer, longerServer server
	baseClient, longerClient *echoServiceClient

	baseServerCalls, longerServerCalls atomic.Int32
	baseClientCalls, longerClientCalls atomic.Int32
}

func (tc *TestCaseSharedInterceptors) Setup(_ context.Context, t *testing.T) {
	// In both pairs, the longer slice is created first and the shorter one is used first, so a write
	// into the shorter slice's spare capacity would overwrite the longer slice's last interceptor
	// before that slice is used.
	serverInterceptors := make([]connect.ServerInterceptor, 0, 4)
	serverInterceptors = append(serverInterceptors, countServerCalls(&tc.baseServerCalls))
	longerServerInterceptors := append(serverInterceptors, countServerCalls(&tc.longerServerCalls))
	baseSrv := connect.NewServer(serverInterceptors...)
	longerSrv := connect.NewServer(longerServerInterceptors...)
	require.Nil(t, serverInterceptors[:cap(serverInterceptors)][len(longerServerInterceptors)], "server interceptors written past the longer slice")
	registerEchoServiceHandler(baseSrv, echoService{})
	tc.baseServer.start(t, baseSrv)
	registerEchoServiceHandler(longerSrv, echoService{})
	tc.longerServer.start(t, longerSrv)

	clientInterceptors := make([]connect.ClientInterceptor, 0, 4)
	clientInterceptors = append(clientInterceptors, countClientCalls(&tc.baseClientCalls))
	longerClientInterceptors := append(clientInterceptors, countClientCalls(&tc.longerClientCalls))
	tc.baseClient = newEchoServiceClient(connect.NewClient(tc.baseServer.transport(), clientInterceptors...))
	tc.longerClient = newEchoServiceClient(connect.NewClient(tc.longerServer.transport(), longerClientInterceptors...))
	require.Nil(t, clientInterceptors[:cap(clientInterceptors)][len(longerClientInterceptors)], "client interceptors written past the longer slice")
}

func (tc *TestCaseSharedInterceptors) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.baseClient)
	callEcho(ctx, t, tc.longerClient)
	tc.baseServer.stop()
	tc.longerServer.stop()
	// Both chains start with the base interceptor; only the longer one has the second interceptor.
	require.Equal(t, int32(2), tc.baseServerCalls.Load(), "base server interceptor calls")
	require.Equal(t, int32(1), tc.longerServerCalls.Load(), "longer server interceptor calls")
	require.Equal(t, int32(2), tc.baseClientCalls.Load(), "base client interceptor calls")
	require.Equal(t, int32(1), tc.longerClientCalls.Load(), "longer client interceptor calls")
}

func (*TestCaseSharedInterceptors) ExpectedSpanCount() int { return 8 }

func (tc *TestCaseSharedInterceptors) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.baseServer.rpcTrace(unaryRPC, 0, 0),
		tc.longerServer.rpcTrace(unaryRPC, 0, 0),
	}
}
