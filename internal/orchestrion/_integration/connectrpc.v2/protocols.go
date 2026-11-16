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

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseGRPCProtocols calls a server with the gRPC and gRPC-Web protocols. Each protocol has its
// own server, so that each trace is told apart by its server's address.
type TestCaseGRPCProtocols struct {
	grpcServer, grpcWebServer server
	grpcClient, grpcWebClient *echoServiceClient
}

func (tc *TestCaseGRPCProtocols) Setup(_ context.Context, t *testing.T) {
	grpcSrv := connect.NewServer()
	registerEchoServiceHandler(grpcSrv, echoService{})
	tc.grpcServer.start(t, grpcSrv)
	grpcWebSrv := connect.NewServer()
	registerEchoServiceHandler(grpcWebSrv, echoService{})
	tc.grpcWebServer.start(t, grpcWebSrv)

	tc.grpcClient = newEchoServiceClient(connect.NewClient(tc.grpcServer.transport(connecthttp.WithGRPC())))
	tc.grpcWebClient = newEchoServiceClient(connect.NewClient(tc.grpcWebServer.transport(connecthttp.WithGRPCWeb())))
}

func (tc *TestCaseGRPCProtocols) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.grpcClient)
	callEcho(ctx, t, tc.grpcWebClient)
	tc.grpcServer.stop()
	tc.grpcWebServer.stop()
}

func (*TestCaseGRPCProtocols) ExpectedSpanCount() int { return 8 }

func (tc *TestCaseGRPCProtocols) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, 2)
	for _, s := range []*server{&tc.grpcServer, &tc.grpcWebServer} {
		traces = append(traces, asGRPC(s.clientCallSpan(unaryRPC, 0,
			s.httpClientSpan(unaryRPC,
				s.httpServerSpan(unaryRPC,
					asGRPC(serverCallSpan(unaryRPC, 0), 0),
				),
			),
		), 0))
	}
	return traces
}
