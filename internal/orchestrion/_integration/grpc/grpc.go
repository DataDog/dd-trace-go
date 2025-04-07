// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/helloworld/helloworld"
)

type TestCase struct {
	*grpc.Server
	addr string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()

	var (
		interceptedDirect atomic.Bool
		interceptedChain  atomic.Bool
	)
	tc.Server = grpc.NewServer(
		// Register a bunch of interceptors to ensure ours does not cause a runtime crash.
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			interceptedDirect.Store(true)
			return handler(ctx, req)
		}),
		grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			interceptedChain.Store(true)
			return handler(ctx, req)
		}),
	)
	helloworld.RegisterGreeterServer(tc.Server, &server{})

	go func() { assert.NoError(t, tc.Server.Serve(lis)) }()
	t.Cleanup(func() {
		tc.Server.GracefulStop()
		assert.True(t, interceptedDirect.Load(), "original interceptor was not called")
		assert.True(t, interceptedChain.Load(), "original chained interceptor was not called")
	})
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	var (
		interceptedDirect atomic.Bool
		interceptedChain  atomic.Bool
	)

	conn, err := grpc.NewClient(
		tc.addr,
		// Register a bunch of interceptors to ensure ours does not cause a runtime crash.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			interceptedDirect.Store(true)
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			interceptedChain.Store(true)
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	client := helloworld.NewGreeterClient(conn)
	resp, err := client.SayHello(ctx, &helloworld.HelloRequest{Name: "rob"})
	require.NoError(t, err)
	require.Equal(t, "Hello rob", resp.GetMessage())

	assert.True(t, interceptedDirect.Load(), "original interceptor was not called")
	assert.True(t, interceptedChain.Load(), "original chained interceptor was not called")
}

func (*TestCase) ExpectedTraces() trace.Traces {
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
