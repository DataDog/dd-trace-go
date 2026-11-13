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

// TestCaseServerSpread builds one server from a spread slice of interceptors and one without
// arguments, which Orchestrion rewrites differently from plain arguments.
type TestCaseServerSpread struct {
	spreadServer, emptyServer server
	spreadClient, emptyClient *echoServiceClient
	serverIntercepted         atomic.Int32
}

func (tc *TestCaseServerSpread) Setup(_ context.Context, t *testing.T) {
	interceptors := []connect.ServerInterceptor{countServerCalls(&tc.serverIntercepted)}
	spreadSrv := connect.NewServer(interceptors...)
	registerEchoServiceHandler(spreadSrv, echoService{})
	tc.spreadServer.start(t, spreadSrv)
	emptySrv := connect.NewServer()
	registerEchoServiceHandler(emptySrv, echoService{})
	tc.emptyServer.start(t, emptySrv)

	tc.spreadClient = newEchoServiceClient(connect.NewClient(tc.spreadServer.transport()))
	tc.emptyClient = newEchoServiceClient(connect.NewClient(tc.emptyServer.transport()))
}

func (tc *TestCaseServerSpread) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.spreadClient)
	callEcho(ctx, t, tc.emptyClient)
	tc.spreadServer.stop()
	tc.emptyServer.stop()
	require.Equal(t, int32(1), tc.serverIntercepted.Load(), "user server interceptor calls")
}

func (*TestCaseServerSpread) ExpectedSpanCount() int { return 8 }

func (tc *TestCaseServerSpread) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.spreadServer.rpcTrace(unaryRPC, 0, 0),
		tc.emptyServer.rpcTrace(unaryRPC, 0, 0),
	}
}
