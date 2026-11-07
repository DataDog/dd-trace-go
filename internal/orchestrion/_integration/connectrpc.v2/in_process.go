// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseInProcess calls a server through the in-process transport: the server span is the child
// of the client span, with no HTTP spans between them.
type TestCaseInProcess struct {
	client *echoServiceClient
}

func (tc *TestCaseInProcess) Setup(context.Context, *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.client = newEchoServiceClient(connect.NewClient(connectinprocess.New(srv)))
}

func (tc *TestCaseInProcess) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.client)
	// The server returns before the client receives io.EOF, so its spans are finished when Run
	// returns.
	callChat(ctx, t, tc.client)
}

func (*TestCaseInProcess) ExpectedSpanCount() int { return 14 }

func (*TestCaseInProcess) ExpectedTraces() trace.Traces {
	return trace.Traces{
		inProcessClientCallSpan(unaryRPC, 0, serverCallSpan(unaryRPC, 0)),
		inProcessClientCallSpan(bidiStreamRPC, 5, serverCallSpan(bidiStreamRPC, 5)),
	}
}
