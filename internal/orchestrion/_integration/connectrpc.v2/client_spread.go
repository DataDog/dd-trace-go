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

// TestCaseClientSpread builds one client from a spread slice of interceptors and one with only a
// transport, which Orchestrion rewrites differently from plain arguments. Each client calls its own
// server, so that each trace is told apart by its server's address.
type TestCaseClientSpread struct {
	spreadServer, emptyServer server
	spreadClient, emptyClient *echoServiceClient
	clientIntercepted         atomic.Int32
}

func (tc *TestCaseClientSpread) Setup(_ context.Context, t *testing.T) {
	for _, s := range []*server{&tc.spreadServer, &tc.emptyServer} {
		srv := connect.NewServer()
		registerEchoServiceHandler(srv, echoService{})
		s.start(t, srv)
	}

	interceptors := []connect.ClientInterceptor{countClientCalls(&tc.clientIntercepted)}
	tc.spreadClient = newEchoServiceClient(connect.NewClient(tc.spreadServer.transport(), interceptors...))
	tc.emptyClient = newEchoServiceClient(connect.NewClient(tc.emptyServer.transport()))
}

func (tc *TestCaseClientSpread) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.spreadClient)
	callEcho(ctx, t, tc.emptyClient)
	tc.spreadServer.stop()
	tc.emptyServer.stop()
	require.Equal(t, int32(1), tc.clientIntercepted.Load(), "user client interceptor calls")
}

func (*TestCaseClientSpread) ExpectedSpanCount() int { return 8 }

func (tc *TestCaseClientSpread) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.spreadServer.rpcTrace(unaryRPC, 0, 0),
		tc.emptyServer.rpcTrace(unaryRPC, 0, 0),
	}
}
