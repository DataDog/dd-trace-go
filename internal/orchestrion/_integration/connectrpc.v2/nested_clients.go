// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// clientTransport is a connect.Transport that opens its streams through another client, as a proxy
// or a decorator of a client would.
type clientTransport struct {
	client *connect.Client
}

func (t clientTransport) NewClientStream(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
	return t.client.CallClientStream(ctx, spec)
}

// TestCaseNestedClients calls through a client whose transport is another client. Both clients are
// woven, so every call goes through two Datadog interceptors, and it must still produce a single
// connect.client span.
type TestCaseNestedClients struct {
	server
	httpClient sendRecorder
	client     *echoServiceClient
}

func (tc *TestCaseNestedClients) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)
	tc.httpClient.client = tc.srv.Client()
	inner := connect.NewClient(connecthttp.NewTransport(&tc.httpClient, tc.srv.URL))
	tc.client = newEchoServiceClient(connect.NewClient(clientTransport{client: inner}))
}

func (tc *TestCaseNestedClients) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.client)
	tc.stop()
	// A second connect.client span would be a child of the first, and active when the request is
	// sent.
	sent := tc.httpClient.last.Load()
	require.NotNil(t, sent)
	require.True(t, sent.traced, "request sent without an active span")
	require.True(t, sent.root, "request sent under nested spans")
}

func (*TestCaseNestedClients) ExpectedSpanCount() int { return 4 }

func (tc *TestCaseNestedClients) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.rpcTrace(unaryRPC, 0, 0)}
}
