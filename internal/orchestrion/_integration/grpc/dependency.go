// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"testing"

	"example.com/grpcdep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/examples/helloworld/helloworld"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a client and a server created in a dependency
// module, not in the module being built, are traced.
type TestCaseDependency struct {
	*grpc.Server
	addr string
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()

	tc.Server = grpcdep.NewServer()
	helloworld.RegisterGreeterServer(tc.Server, &server{})

	go func() { assert.NoError(t, tc.Server.Serve(lis)) }()
	t.Cleanup(tc.Server.GracefulStop)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	conn, err := grpcdep.NewClient(tc.addr)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	resp, err := helloworld.NewGreeterClient(conn).SayHello(ctx, &helloworld.HelloRequest{Name: "rob"})
	require.NoError(t, err)
	require.Equal(t, "Hello rob", resp.GetMessage())
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return new(TestCase).ExpectedTraces()
}
