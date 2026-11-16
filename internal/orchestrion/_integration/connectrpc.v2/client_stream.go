// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseClientStream reads the response like the generated CloseAndReceive, which never calls
// Close, on a context that never ends: the client span must still finish.
type TestCaseClientStream struct {
	server
	client *echoServiceClient
}

func (tc *TestCaseClientStream) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)
	tc.client = newEchoServiceClient(connect.NewClient(tc.transport()))
}

func (tc *TestCaseClientStream) Run(_ context.Context, t *testing.T) {
	stream, err := tc.client.Collect(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(wrapperspb.String("hello")))
	require.NoError(t, stream.Send(wrapperspb.String(" world")))
	res, err := stream.CloseAndReceive()
	require.NoError(t, err)
	require.Equal(t, "hello world", res.GetValue())
	tc.stop()
}

func (*TestCaseClientStream) ExpectedSpanCount() int { return 11 }

func (tc *TestCaseClientStream) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Client: Send x2, then the Receive of the response. Server: Receive x3 (two requests and
		// the EOF), then the response Send.
		tc.rpcTrace(clientStreamRPC, 3, 4),
	}
}
