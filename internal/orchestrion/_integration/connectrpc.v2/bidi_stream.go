// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseBidiStream struct {
	server
	client *echoServiceClient
}

func (tc *TestCaseBidiStream) Setup(_ context.Context, t *testing.T) {
	srv := connect.NewServer()
	registerEchoServiceHandler(srv, echoService{})
	tc.start(t, srv)
	tc.client = newEchoServiceClient(connect.NewClient(tc.transport()))
}

func (tc *TestCaseBidiStream) Run(ctx context.Context, t *testing.T) {
	callChat(ctx, t, tc.client)
	tc.stop()
}

func (*TestCaseBidiStream) ExpectedSpanCount() int { return 14 }

func (tc *TestCaseBidiStream) ExpectedTraces() trace.Traces {
	return trace.Traces{
		// Each side: Send x2 and Receive x3 (two messages and the EOF).
		tc.rpcTrace(bidiStreamRPC, 5, 5),
	}
}
