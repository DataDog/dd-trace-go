// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpcreflect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

var reflectionRPC = newRPC(grpcreflect.ReflectV1ServiceName, "ServerReflectionInfo", connect.StreamTypeBidi)

// TestCaseGRPCReflect uses the grpcreflect handlers and client. The client stream sends its headers
// eagerly with SendHeaders, and Close reads the stream to io.EOF before closing it.
type TestCaseGRPCReflect struct {
	server
	client *grpcreflect.Client
}

func (tc *TestCaseGRPCReflect) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	grpcreflect.Register(srv)
	tc.start(t, srv)
	tc.client = grpcreflect.NewClient(connect.NewClient(tc.transport(connecthttp.WithGRPC())))
}

func (tc *TestCaseGRPCReflect) Run(ctx context.Context, t *testing.T) {
	stream := tc.client.NewStream(ctx)
	services, err := stream.ListServices()
	require.NoError(t, err)
	require.Equal(t, []protoreflect.FullName{grpcreflect.ReflectV1ServiceName, grpcreflect.ReflectV1AlphaServiceName}, services)
	require.NoError(t, stream.Close())
	tc.stop()
}

func (*TestCaseGRPCReflect) ExpectedSpanCount() int { return 10 }

func (tc *TestCaseGRPCReflect) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Client: Send, Receive, then Close's Receive of the EOF; SendHeaders has no span. Server:
		// Receive, Send, then the Receive of the EOF.
		asGRPC(tc.clientCallSpan(reflectionRPC, 3,
			tc.httpClientSpan(reflectionRPC,
				tc.httpServerSpan(reflectionRPC,
					asGRPC(serverCallSpan(reflectionRPC, 3), 0),
				),
			),
		), 0),
	}
}
