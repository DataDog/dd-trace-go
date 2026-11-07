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

var (
	// unknownRPC is a unary procedure of the example service that the server doesn't register.
	unknownRPC = newRPC(echoServiceName, "Unknown", connect.StreamTypeUnary)
	// unknownHandlerRPC is unknownRPC as the server's unknown handler sees it: a bidi stream.
	unknownHandlerRPC = newRPC(echoServiceName, "Unknown", connect.StreamTypeBidi)
)

// TestCaseUnknownHandler calls a procedure that the server serves with its unknown handler.
type TestCaseUnknownHandler struct {
	server
	client *connect.Client
}

func (tc *TestCaseUnknownHandler) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	// connecthttp routes the unknown methods of registered services to the unknown handler.
	registerEchoServiceHandler(srv, echoService{})
	srv.SetUnknownHandler(func(_ context.Context, spec connect.Spec, stream connect.ServerStream) error {
		var req wrapperspb.StringValue
		if err := stream.Receive(&req); err != nil {
			return err
		}
		return stream.Send(wrapperspb.String(spec.Procedure))
	})
	tc.start(t, srv)
	tc.client = connect.NewClient(tc.transport())
}

func (tc *TestCaseUnknownHandler) Run(ctx context.Context, t *testing.T) {
	var res wrapperspb.StringValue
	require.NoError(t, tc.client.CallUnary(ctx, unknownRPC.spec, wrapperspb.String("hello"), &res))
	require.Equal(t, unknownRPC.procedure(), res.GetValue())
	tc.stop()
}

func (*TestCaseUnknownHandler) ExpectedSpanCount() int { return 6 }

func (tc *TestCaseUnknownHandler) ExpectedTraces() trace.Traces {
	// The request Receive and the response Send.
	serverSpan := serverCallSpan(unknownHandlerRPC, 2)
	// The client chose the procedure: the server's spans are those of an unrecognized method.
	for _, span := range append([]*trace.Trace{serverSpan}, serverSpan.Children...) {
		span.Tags["resource"] = "_OTHER"
		span.Meta["rpc.method"] = "_OTHER"
		span.Meta["rpc.method_original"] = unknownHandlerRPC.method
	}
	httpServer := tc.httpServerSpan(unknownRPC, serverSpan)
	// connecthttp.Mount routes the service's unknown methods with a pattern for the whole service.
	route := "/" + echoServiceName + "/"
	httpServer.Tags["resource"] = "POST " + route
	httpServer.Meta["http.route"] = route
	return trace.Traces{
		tc.clientCallSpan(unknownRPC, 0,
			tc.httpClientSpan(unknownRPC, httpServer),
		),
	}
}
