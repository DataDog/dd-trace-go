// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSharedOptions builds two clients, and two handlers, from option slices that share a
// backing array with spare capacity. The woven code must not append into that array: building the
// first client or handler would otherwise overwrite the option the second one adds.
type TestCaseSharedOptions struct {
	protoServer, jsonServer server
	protoHTTP, jsonHTTP     sendRecorder
	protoClient, jsonClient *echoClient
	clientIntercepted       atomic.Int32
	serverIntercepted       atomic.Int32
}

// echoContentType responds with the request's Content-Type, which identifies the client's codec.
func echoContentType(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
	return connect.NewResponse(wrapperspb.String(req.Header().Get("Content-Type"))), nil
}

func countCalls(n *atomic.Int32) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			n.Add(1)
			return next(ctx, req)
		}
	}))
}

func (tc *TestCaseSharedOptions) Setup(_ context.Context, t *testing.T) {
	// In both pairs, the shorter slice is used first, so an append into its spare capacity would
	// overwrite the longer slice's last option before that slice is used.
	handlerOpts := make([]connect.HandlerOption, 0, 4)
	countingHandlerOpts := append(handlerOpts, countCalls(&tc.serverIntercepted))
	tc.protoServer.start(t, connect.NewUnaryHandler(unaryRPC.procedure, echoContentType, handlerOpts...))
	tc.jsonServer.start(t, connect.NewUnaryHandler(unaryRPC.procedure, echoContentType, countingHandlerOpts...))

	clientOpts := make([]connect.ClientOption, 0, 4)
	clientOpts = append(clientOpts, countCalls(&tc.clientIntercepted))
	jsonClientOpts := append(clientOpts, connect.WithProtoJSON())
	tc.protoHTTP.client = tc.protoServer.srv.Client()
	tc.jsonHTTP.client = tc.jsonServer.srv.Client()
	tc.protoClient = newEchoServiceClient(&tc.protoHTTP, tc.protoServer.srv.URL, clientOpts...)
	tc.jsonClient = newEchoServiceClient(&tc.jsonHTTP, tc.jsonServer.srv.URL, jsonClientOpts...)
}

func (tc *TestCaseSharedOptions) Run(ctx context.Context, t *testing.T) {
	for _, c := range []struct {
		client      *echoClient
		httpClient  *sendRecorder
		contentType string
	}{
		{tc.protoClient, &tc.protoHTTP, "application/proto"},
		{tc.jsonClient, &tc.jsonHTTP, "application/json"},
	} {
		res, err := c.client.CallUnary(ctx, connect.NewRequest(wrapperspb.String("hello")))
		require.NoError(t, err)
		require.Equal(t, c.contentType, res.Msg.GetValue())
		// A second connect.client span would be active when the request is sent.
		sent := c.httpClient.last.Load()
		require.NotNil(t, sent)
		require.True(t, sent.traced, "request sent without an active span")
		require.True(t, sent.root, "request sent under nested spans")
	}
	tc.protoServer.stop()
	tc.jsonServer.stop()
	require.Equal(t, int32(2), tc.clientIntercepted.Load(), "user client interceptor calls")
	require.Equal(t, int32(1), tc.serverIntercepted.Load(), "user handler interceptor calls")
}

func (tc *TestCaseSharedOptions) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, 2)
	for _, s := range []*server{&tc.protoServer, &tc.jsonServer} {
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
