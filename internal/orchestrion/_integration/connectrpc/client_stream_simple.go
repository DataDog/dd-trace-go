// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseClientStreamSimple struct {
	server
	client *echoClient
}

func (tc *TestCaseClientStreamSimple) Setup(_ context.Context, t *testing.T) {
	tc.start(t, connect.NewClientStreamHandlerSimple(
		clientStreamRPC.procedure,
		func(_ context.Context, stream *connect.ClientStream[wrapperspb.StringValue]) (*wrapperspb.StringValue, error) {
			var b strings.Builder
			for stream.Receive() {
				b.WriteString(stream.Msg().GetValue())
			}
			if err := stream.Err(); err != nil {
				return nil, err
			}
			return wrapperspb.String(b.String()), nil
		},
	))
	tc.client = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](tc.srv.Client(), tc.url(clientStreamRPC), connect.WithClientOptions())
}

func (tc *TestCaseClientStreamSimple) Run(ctx context.Context, t *testing.T) {
	stream, err := tc.client.CallClientStreamSimple(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(wrapperspb.String("hello")))
	require.NoError(t, stream.Send(wrapperspb.String(" world")))
	res, err := stream.CloseAndReceive()
	require.NoError(t, err)
	require.Equal(t, "hello world", res.GetValue())
	tc.stop()
}

func (tc *TestCaseClientStreamSimple) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Send x2, then CloseAndReceive's Receive x2 (the response and the EOF). The header-flushing
		// Send(nil) made by CallClientStreamSimple sends no message and gets no span.
		tc.clientCallSpan(clientStreamRPC, 4,
			tc.httpClientSpan(clientStreamRPC,
				tc.httpServerSpan(clientStreamRPC,
					// Receive x3 (two requests and the EOF), then the response Send.
					serverCallSpan(clientStreamRPC, 4),
				),
			),
		),
	}
}
