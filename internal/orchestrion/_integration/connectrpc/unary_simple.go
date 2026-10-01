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

type TestCaseUnarySimple struct {
	server
	client *echoClient
}

func (tc *TestCaseUnarySimple) Setup(_ context.Context, t *testing.T) {
	tc.start(t, connect.NewUnaryHandlerSimple(
		unaryRPC.procedure,
		func(_ context.Context, req *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return wrapperspb.String(req.GetValue()), nil
		},
	))
	tc.client = connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](tc.srv.Client(), tc.url(unaryRPC), connect.WithClientOptions())
}

func (tc *TestCaseUnarySimple) Run(ctx context.Context, t *testing.T) {
	res, err := tc.client.CallUnary(ctx, connect.NewRequest(wrapperspb.String("hello")))
	require.NoError(t, err)
	require.Equal(t, "hello", res.Msg.GetValue())
	tc.stop()
}

func (tc *TestCaseUnarySimple) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.clientCallSpan(unaryRPC, 0,
			tc.httpClientSpan(unaryRPC,
				tc.httpServerSpan(unaryRPC,
					serverCallSpan(unaryRPC, 0),
				),
			),
		),
	}
}
