// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseServerStream struct {
	server
	client *echoClient
}

func (tc *TestCaseServerStream) Setup(_ context.Context, t *testing.T) {
	tc.start(t, connect.NewServerStreamHandler(
		serverStreamRPC.procedure,
		func(_ context.Context, req *connect.Request[wrapperspb.StringValue], stream *connect.ServerStream[wrapperspb.StringValue]) error {
			for _, suffix := range []string{"-1", "-2"} {
				if err := stream.Send(wrapperspb.String(req.Msg.GetValue() + suffix)); err != nil {
					return err
				}
			}
			return nil
		},
	))
	tc.client = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](tc.srv.Client(), tc.url(serverStreamRPC), connect.WithClientOptions())
}

func (tc *TestCaseServerStream) Run(ctx context.Context, t *testing.T) {
	callServerStream(ctx, t, tc.client)
	tc.stop()
}

func (tc *TestCaseServerStream) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// The request Send, then Receive x3 (two responses and the EOF).
		tc.clientCallSpan(serverStreamRPC, 4,
			tc.httpClientSpan(serverStreamRPC,
				tc.httpServerSpan(serverStreamRPC,
					// Receive x2 (the request and the EOF), then Send x2.
					serverCallSpan(serverStreamRPC, 4),
				),
			),
		),
	}
}
