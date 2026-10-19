// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"

	"github.com/go-kratos/kratos/v3/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type GRPCTestCase struct {
	server *kratosgrpc.Server
	addr   string
	called atomic.Bool
}

func (*GRPCTestCase) ExpectedSpanCount() int { return 2 }

func (tc *GRPCTestCase) Setup(ctx context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()
	tc.server = kratosgrpc.NewServer(kratosgrpc.Listener(lis), kratosgrpc.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tc.called.Store(true)
			return next(ctx, req)
		}
	}))
	go func() { _ = tc.server.Start(ctx) }()
	t.Cleanup(func() { require.NoError(t, tc.server.Stop(context.Background())) })
}

func (tc *GRPCTestCase) Run(ctx context.Context, t *testing.T) {
	conn, err := kratosgrpc.NewClient(ctx, kratosgrpc.WithEndpoint(tc.addr))
	require.NoError(t, err)
	defer conn.Close()
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.True(t, tc.called.Load(), "caller server middleware must be preserved")
}

func (*GRPCTestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "kratos.client.request"}, Children: trace.Traces{{Tags: map[string]any{"name": "kratos.server.request"}}}}}
}

type StreamCase struct {
	server *kratosgrpc.Server
	addr   string
}

type healthStreamServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (*healthStreamServer) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	return stream.Send(&grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING})
}

func (tc *StreamCase) Setup(ctx context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()
	tc.server = kratosgrpc.NewServer(kratosgrpc.Listener(lis), kratosgrpc.CustomHealth())
	grpc_health_v1.RegisterHealthServer(tc.server, new(healthStreamServer))
	go func() { _ = tc.server.Start(ctx) }()
	t.Cleanup(func() { require.NoError(t, tc.server.Stop(context.Background())) })
}

func (tc *StreamCase) Run(ctx context.Context, t *testing.T) {
	conn, err := kratosgrpc.NewClient(ctx, kratosgrpc.WithEndpoint(tc.addr), kratosgrpc.WithHealthCheck(false))
	require.NoError(t, err)
	defer conn.Close()
	stream, err := grpc_health_v1.NewHealthClient(conn).Watch(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func (*StreamCase) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "grpc.client", "resource": "/grpc.health.v1.Health/Watch"}, Children: trace.Traces{{Tags: map[string]any{"name": "grpc.server", "resource": "/grpc.health.v1.Health/Watch"}}}}}
}
