// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"errors"
	"io"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseServerStream struct {
	server
	client *echoServiceClient
}

func (tc *TestCaseServerStream) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)
	tc.client = newEchoServiceClient(connect.NewClient(tc.transport()))
}

func (tc *TestCaseServerStream) Run(ctx context.Context, t *testing.T) {
	stream, err := tc.client.Expand(ctx, wrapperspb.String("hello"))
	require.NoError(t, err)
	var got []string
	for {
		res, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		got = append(got, res.GetValue())
	}
	require.NoError(t, stream.Close())
	require.Equal(t, []string{"hello-1", "hello-2"}, got)
	tc.stop()
}

func (*TestCaseServerStream) ExpectedSpanCount() int { return 11 }

func (tc *TestCaseServerStream) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Client: the request Send, then Receive x3 (two responses and the EOF). Server: the
		// request Receive, then Send x2.
		tc.rpcTrace(serverStreamRPC, 4, 3),
	}
}
