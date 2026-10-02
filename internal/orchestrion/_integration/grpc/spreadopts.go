// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product contains software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/helloworld/helloworld"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSpreadOpts verifies the append-args weaving of grpc.NewServer and
// grpc.NewClient when options are spread from a slice. The option slices have
// spare capacity (len < cap): the injected interceptor options must never leak
// into it, which would silently mutate the caller's slice or race across
// concurrent calls sharing it (DataDog/dd-trace-go#5486).
type TestCaseSpreadOpts struct {
	*grpc.Server
	addr string
}

func (tc *TestCaseSpreadOpts) Setup(_ context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()

	var (
		interceptedDirect atomic.Bool
		interceptedChain  atomic.Bool
	)
	serverOpts := make([]grpc.ServerOption, 2, 5)
	serverOpts[0] = grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		interceptedDirect.Store(true)
		return handler(ctx, req)
	})
	serverOpts[1] = grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		interceptedChain.Store(true)
		return handler(ctx, req)
	})
	serverSpare := serverOpts[:cap(serverOpts)]
	tc.Server = grpc.NewServer(serverOpts...)
	require.Len(t, serverOpts, 2, "woven call must not grow the caller's option slice")
	assert.Nil(t, serverSpare[2], "woven call must not write into the caller's spare capacity")

	helloworld.RegisterGreeterServer(tc.Server, &server{})

	go func() { assert.NoError(t, tc.Server.Serve(lis)) }()
	t.Cleanup(func() {
		tc.Server.GracefulStop()
		assert.True(t, interceptedDirect.Load(), "original interceptor was not called")
		assert.True(t, interceptedChain.Load(), "original chained interceptor was not called")
	})
}

func (tc *TestCaseSpreadOpts) Run(ctx context.Context, t *testing.T) {
	var (
		interceptedDirect atomic.Bool
		interceptedChain  atomic.Bool
	)

	clientOpts := make([]grpc.DialOption, 3, 6)
	clientOpts[0] = grpc.WithTransportCredentials(insecure.NewCredentials())
	clientOpts[1] = grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		interceptedDirect.Store(true)
		return invoker(ctx, method, req, reply, cc, opts...)
	})
	clientOpts[2] = grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		interceptedChain.Store(true)
		return invoker(ctx, method, req, reply, cc, opts...)
	})
	clientSpare := clientOpts[:cap(clientOpts)]
	conn, err := grpc.NewClient(tc.addr, clientOpts...)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	require.Len(t, clientOpts, 3, "woven call must not grow the caller's option slice")
	assert.Nil(t, clientSpare[3], "woven call must not write into the caller's spare capacity")

	client := helloworld.NewGreeterClient(conn)
	resp, err := client.SayHello(ctx, &helloworld.HelloRequest{Name: "rob"})
	require.NoError(t, err)
	require.Equal(t, "Hello rob", resp.GetMessage())

	assert.True(t, interceptedDirect.Load(), "original interceptor was not called")
	assert.True(t, interceptedChain.Load(), "original chained interceptor was not called")
}

func (*TestCaseSpreadOpts) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "grpc.client",
				"service":  "grpc.client",
				"resource": "/helloworld.Greeter/SayHello",
				"type":     "rpc",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "grpc.server",
						"service":  "grpc.server",
						"resource": "/helloworld.Greeter/SayHello",
						"type":     "rpc",
					},
				},
			},
		},
	}
}
