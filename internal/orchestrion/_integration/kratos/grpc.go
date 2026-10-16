// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"net"
	"testing"

	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type GRPCTestCase struct {
	server *kratosgrpc.Server
	addr   string
}

func (*GRPCTestCase) ExpectedSpanCount() int { return 2 }

func (tc *GRPCTestCase) Setup(ctx context.Context, t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = lis.Addr().String()
	tc.server = kratosgrpc.NewServer(kratosgrpc.Listener(lis))
	go func() { _ = tc.server.Start(ctx) }()
	t.Cleanup(func() { require.NoError(t, tc.server.Stop(context.Background())) })
}

func (tc *GRPCTestCase) Run(ctx context.Context, t *testing.T) {
	conn, err := kratosgrpc.NewClient(ctx, kratosgrpc.WithEndpoint(tc.addr))
	require.NoError(t, err)
	defer conn.Close()
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
}

func (*GRPCTestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "kratos.client.request"}, Children: trace.Traces{{Tags: map[string]any{"name": "kratos.server.request"}}}}}
}
