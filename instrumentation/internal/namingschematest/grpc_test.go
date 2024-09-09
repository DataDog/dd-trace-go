// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"net"
	"testing"
	"time"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	grpcServerTest = harness.TestCase{
		Name:     instrumentation.PackageGRPC + "_server",
		GenSpans: grpcGenSpansFn(false, true),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        harness.RepeatString("grpc.server", 4),
			DDService:       harness.RepeatString(harness.TestDDService, 4),
			ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 4),
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			for i := 0; i < 4; i++ {
				assert.Equal(t, "grpc.server", spans[i].OperationName())
			}
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			for i := 0; i < 4; i++ {
				assert.Equal(t, "grpc.server.request", spans[i].OperationName())
			}
		},
	}
	grpcClientTest = harness.TestCase{
		Name:     instrumentation.PackageGRPC + "_client",
		GenSpans: grpcGenSpansFn(true, false),
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        harness.RepeatString("grpc.client", 4),
			DDService:       harness.RepeatString("grpc.client", 4),
			ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 4),
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			for i := 0; i < 4; i++ {
				assert.Equal(t, "grpc.client", spans[i].OperationName())
			}
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			for i := 0; i < 4; i++ {
				assert.Equal(t, "grpc.client.request", spans[i].OperationName())
			}
		},
	}
)

func grpcGenSpansFn(traceClient, traceServer bool) harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []grpctrace.Option
		if serviceOverride != "" {
			opts = append(opts, grpctrace.WithService(serviceOverride))
		}
		// exclude the grpc.message spans as they are not affected by naming schema
		opts = append(opts, grpctrace.WithStreamMessages(false))
		mt := mocktracer.Start()
		defer mt.Stop()

		var serverInterceptors []grpc.ServerOption
		if traceServer {
			serverInterceptors = append(serverInterceptors,
				grpc.UnaryInterceptor(grpctrace.UnaryServerInterceptor(opts...)),
				grpc.StreamInterceptor(grpctrace.StreamServerInterceptor(opts...)),
				grpc.StatsHandler(grpctrace.NewServerStatsHandler(opts...)),
			)
		}
		clientInterceptors := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		if traceClient {
			clientInterceptors = append(clientInterceptors,
				grpc.WithUnaryInterceptor(grpctrace.UnaryClientInterceptor(opts...)),
				grpc.WithStreamInterceptor(grpctrace.StreamClientInterceptor(opts...)),
				grpc.WithStatsHandler(grpctrace.NewClientStatsHandler(opts...)),
			)
		}
		client := startGRPCTestServer(t, serverInterceptors, clientInterceptors)
		_, err := client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "pass"})
		require.NoError(t, err)

		stream, err := client.StreamPing(context.Background())
		require.NoError(t, err)
		err = stream.Send(&fixturepb.FixtureRequest{Name: "break"})
		require.NoError(t, err)
		_, err = stream.Recv()
		require.NoError(t, err)
		err = stream.CloseSend()
		require.NoError(t, err)
		// to flush the spans
		_, _ = stream.Recv()

		waitForSpans(t, mt, 4, 2*time.Second)
		return mt.FinishedSpans()
	}
}

func startGRPCTestServer(
	t *testing.T,
	serverInterceptors []grpc.ServerOption,
	clientInterceptors []grpc.DialOption,
) fixturepb.FixtureClient {
	server := grpc.NewServer(serverInterceptors...)
	srv := fixturepb.NewFixtureServer()
	fixturepb.RegisterFixtureServer(server, srv)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test server
	go server.Serve(li)
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("localhost:"+port, clientInterceptors...)
	require.NoError(t, err)

	return fixturepb.NewFixtureClient(conn)
}

func waitForSpans(t *testing.T, mt mocktracer.Tracer, minSpans int, timeout time.Duration) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if len(mt.FinishedSpans()) >= minSpans {
			return
		}
		select {
		case <-ticker.C:
			continue
		case <-timeoutChan:
			assert.FailNow(t, "timeout waiting for spans", "(want: %d, got: %d)", minSpans, len(mt.FinishedSpans()))
		}
	}
}
