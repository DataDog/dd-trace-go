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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseBidiStream struct {
	server
	client *echoClient
}

func (tc *TestCaseBidiStream) Setup(_ context.Context, t *testing.T) {
	tc.start(t, connect.NewBidiStreamHandler(
		bidiStreamRPC.procedure,
		func(_ context.Context, stream *connect.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
			for {
				msg, err := stream.Receive()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := stream.Send(wrapperspb.String("echo: " + msg.GetValue())); err != nil {
					return err
				}
			}
		},
	))
	tc.client = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](tc.srv.Client(), tc.url(bidiStreamRPC), connect.WithClientOptions())
}

func (tc *TestCaseBidiStream) Run(ctx context.Context, t *testing.T) {
	stream := tc.client.CallBidiStream(ctx)
	for _, msg := range []string{"ping", "pong"} {
		require.NoError(t, stream.Send(wrapperspb.String(msg)))
		res, err := stream.Receive()
		require.NoError(t, err)
		require.Equal(t, "echo: "+msg, res.GetValue())
	}
	require.NoError(t, stream.CloseRequest())
	_, err := stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.CloseResponse())
	tc.stop()
}

func (tc *TestCaseBidiStream) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Send x2, then Receive x3 (two responses and the EOF).
		tc.clientCallSpan(bidiStreamRPC, 5,
			tc.httpClientSpan(bidiStreamRPC,
				tc.httpServerSpan(bidiStreamRPC,
					// Receive x3 (two requests and the EOF), then Send x2.
					serverCallSpan(bidiStreamRPC, 5),
				),
			),
		),
	}
}
