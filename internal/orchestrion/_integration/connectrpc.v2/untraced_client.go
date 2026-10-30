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

// TestCaseUntracedClient opts its client out of instrumentation, so only the server side is traced.
type TestCaseUntracedClient struct {
	server
	httpClient sendRecorder
	client     *echoServiceClient
}

func (tc *TestCaseUntracedClient) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)
	tc.httpClient.client = tc.srv.Client()
	//orchestrion:ignore
	client := connect.NewClient(connecthttp.NewTransport(&tc.httpClient, tc.srv.URL))
	tc.client = newEchoServiceClient(client)
}

func (tc *TestCaseUntracedClient) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.client)
	tc.stop()
	sent := tc.httpClient.last.Load()
	require.NotNil(t, sent)
	require.False(t, sent.traced, "request sent under a span")
}

func (*TestCaseUntracedClient) ExpectedSpanCount() int { return 3 }

func (tc *TestCaseUntracedClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.httpClientSpan(unaryRPC,
			tc.httpServerSpan(unaryRPC,
				serverCallSpan(unaryRPC, 0),
			),
		),
	}
}
