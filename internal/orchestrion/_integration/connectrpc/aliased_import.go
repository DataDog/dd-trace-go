// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"sync/atomic"
	"testing"

	connectv1 "connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type (
	handlerOptionList []connectv1.HandlerOption
	clientOptionList  []connectv1.ClientOption
)

// TestCaseAliasedImport imports connect under another name, shadows the names of the packages that
// the woven code uses, and spreads nil and slices of named types, which the woven code must all
// compile with.
type TestCaseAliasedImport struct {
	nilServer, namedServer server
	nilClient, namedClient *echoClient
	serverIntercepted      atomic.Int32
	clientIntercepted      atomic.Int32
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	connect, connecttrace := "connect", "connecttrace"
	_, _ = connect, connecttrace

	tc.nilServer.start(t, connectv1.NewUnaryHandler(unaryRPC.procedure, echo, nil...))
	handlerOpts := handlerOptionList{countCalls(&tc.serverIntercepted)}
	tc.namedServer.start(t, connectv1.NewUnaryHandler(unaryRPC.procedure, echo, handlerOpts...))

	tc.nilClient = connectv1.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		tc.nilServer.srv.Client(),
		tc.nilServer.url(unaryRPC),
		connectv1.WithClientOptions(nil...),
	)
	clientOpts := clientOptionList{countCalls(&tc.clientIntercepted)}
	tc.namedClient = connectv1.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		tc.namedServer.srv.Client(),
		tc.namedServer.url(unaryRPC),
		connectv1.WithClientOptions(clientOpts...),
	)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	for _, client := range []*echoClient{tc.nilClient, tc.namedClient} {
		res, err := client.CallUnary(ctx, connectv1.NewRequest(wrapperspb.String("hello")))
		require.NoError(t, err)
		require.Equal(t, "hello", res.Msg.GetValue())
	}
	tc.nilServer.stop()
	tc.namedServer.stop()
	require.Equal(t, int32(1), tc.serverIntercepted.Load(), "user handler interceptor calls")
	require.Equal(t, int32(1), tc.clientIntercepted.Load(), "user client interceptor calls")
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int { return 8 }

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, 2)
	for _, s := range []*server{&tc.nilServer, &tc.namedServer} {
		traces = append(traces, s.clientCallSpan(unaryRPC, 0,
			s.httpClientSpan(unaryRPC,
				s.httpServerSpan(unaryRPC,
					serverCallSpan(unaryRPC, 0),
				),
			),
		))
	}
	return traces
}
