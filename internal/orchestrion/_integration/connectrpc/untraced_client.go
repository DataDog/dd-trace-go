// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseUntracedClient builds its client without connect.WithClientOptions, which is what the
// client aspect matches, so only the server side is traced.
type TestCaseUntracedClient struct {
	server
	httpClient sendRecorder
	client     *echoClient
}

func (tc *TestCaseUntracedClient) Setup(_ context.Context, t *testing.T) {
	tc.start(t, connect.NewUnaryHandler(unaryRPC.procedure, echo))
	tc.httpClient.client = tc.srv.Client()
	tc.client = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](&tc.httpClient, tc.url(unaryRPC))
}

func (tc *TestCaseUntracedClient) Run(ctx context.Context, t *testing.T) {
	res, err := tc.client.CallUnary(ctx, connect.NewRequest(wrapperspb.String("hello")))
	require.NoError(t, err)
	require.Equal(t, "hello", res.Msg.GetValue())
	tc.stop()
	// The harness cannot assert that a span is absent, so check that no connect.client span was
	// active when the request was sent.
	sent := tc.httpClient.last.Load()
	require.NotNil(t, sent)
	require.False(t, sent.traced, "request sent under a span")
}

func (tc *TestCaseUntracedClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.httpClientSpan(unaryRPC,
			tc.httpServerSpan(unaryRPC,
				serverCallSpan(unaryRPC, 0),
			),
		),
	}
}
