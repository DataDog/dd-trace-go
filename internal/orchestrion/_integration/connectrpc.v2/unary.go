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
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase builds its client and server with an interceptor passed as a plain argument, and uses
// them through the shapes of generated code.
type TestCase struct {
	server
	httpClient        sendRecorder
	client            *echoServiceClient
	clientIntercepted atomic.Int32
	serverIntercepted atomic.Int32
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer(countServerCalls(&tc.serverIntercepted))
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)

	tc.httpClient.client = tc.srv.Client()
	tc.client = newEchoServiceClient(connect.NewClient(
		connecthttp.NewTransport(&tc.httpClient, tc.srv.URL),
		countClientCalls(&tc.clientIntercepted),
	))
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.client)
	tc.stop()
	require.Equal(t, int32(1), tc.clientIntercepted.Load(), "user client interceptor calls")
	require.Equal(t, int32(1), tc.serverIntercepted.Load(), "user server interceptor calls")
	// The connect.client span is the trace root, and it must be active when the request is sent.
	sent := tc.httpClient.last.Load()
	require.NotNil(t, sent)
	require.True(t, sent.traced, "request sent without an active span")
	require.True(t, sent.root, "request sent under nested spans")
}

func (*TestCase) ExpectedSpanCount() int { return 4 }

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.rpcTrace(unaryRPC, 0, 0)}
}
