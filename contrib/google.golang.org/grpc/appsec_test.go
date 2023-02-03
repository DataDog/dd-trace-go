// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	rig, err := newRig(false)
	require.NoError(t, err)
	defer rig.Close()

	client := rig.client

	t.Run("unary", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		res, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})
		// Check that the handler was properly called
		require.NoError(t, err)
		require.Equal(t, "passed", res.Message)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-941-110")) // XSS attack attempt
		require.True(t, strings.Contains(event, "ua0-600-55x")) // canary rule attack attempt
	})

	t.Run("stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send a XSS attack
		err = stream.Send(&FixtureRequest{Name: "<script>alert('xss');</script>"})
		require.NoError(t, err)

		// Check that the handler was properly called
		res, err := stream.Recv()
		require.Equal(t, "passed", res.Message)
		require.NoError(t, err)

		// Send a SQLi attack
		err = stream.Send(&FixtureRequest{Name: "something UNION SELECT * from users"})
		require.NoError(t, err)

		// Check that the handler was properly called
		res, err = stream.Recv()
		require.Equal(t, "passed", res.Message)
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		// to flush the spans
		stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 6)

		// The request should have the attack attempts
		event, _ := finished[5].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-941-110")) // XSS attack attempt
		require.True(t, strings.Contains(event, "crs-942-100")) // SQL-injection attack attempt
		require.True(t, strings.Contains(event, "ua0-600-55x")) // canary rule attack attempt
	})
}

// Test that http blocking works by using custom rules/rules data
func TestBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	rig, err := newRig(false)
	require.NoError(t, err)
	defer rig.Close()

	client := rig.client

	t.Run("unary-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log", "x-client-ip", "1.2.3.4"))
		reply, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})

		require.Nil(t, reply)
		require.Equal(t, codes.Aborted, status.Code(err))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "blk-001-001"))
	})

	t.Run("unary-no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log", "x-client-ip", "1.2.3.5"))
		reply, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})

		require.Equal(t, "passed", reply.Message)
		require.Equal(t, codes.OK, status.Code(err))
	})

	t.Run("stream-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log", "x-client-ip", "1.2.3.4"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)
		reply, err := stream.Recv()

		require.Equal(t, codes.Aborted, status.Code(err))
		require.Nil(t, reply)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "blk-001-001"))
	})

	t.Run("stream-no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log", "x-client-ip", "1.2.3.5"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send a XSS attack
		err = stream.Send(&FixtureRequest{Name: "<script>alert('xss');</script>"})
		require.NoError(t, err)
		reply, err := stream.Recv()
		require.Equal(t, codes.OK, status.Code(err))
		require.Equal(t, "passed", reply.Message)

		err = stream.CloseSend()
		require.NoError(t, err)
	})

}

// Test that user blocking works by using custom rules/rules data
func TestUserBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	rig, err := newAppsecRig(false)
	require.NoError(t, err)
	defer rig.Close()
	client := rig.client

	t.Run("unary-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("user-id", "blocked-user-1"))
		reply, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})

		require.Nil(t, reply)
		require.Equal(t, codes.Aborted, status.Code(err))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "blk-001-002"))
	})

	t.Run("unary-no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("user-id", "legit user"))
		reply, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})

		require.Equal(t, "passed", reply.Message)
		require.Equal(t, codes.OK, status.Code(err))
	})

	t.Run("stream-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("user-id", "blocked-user-1"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)
		reply, err := stream.Recv()

		require.Equal(t, codes.Aborted, status.Code(err))
		require.Nil(t, reply)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "blk-001-002"))
	})

	t.Run("stream-no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("user-id", "legit user"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send a XSS attack
		err = stream.Send(&FixtureRequest{Name: "<script>alert('xss');</script>"})
		require.NoError(t, err)
		reply, err := stream.Recv()
		require.Equal(t, codes.OK, status.Code(err))
		require.Equal(t, "passed", reply.Message)

		err = stream.CloseSend()
		require.NoError(t, err)
	})
}

func newAppsecRig(traceClient bool, interceptorOpts ...Option) (*appsecRig, error) {
	interceptorOpts = append([]InterceptorOption{WithServiceName("grpc")}, interceptorOpts...)

	server := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryServerInterceptor(interceptorOpts...)),
		grpc.StreamInterceptor(StreamServerInterceptor(interceptorOpts...)),
	)

	fixtureServer := new(appsecFixtureServer)
	RegisterFixtureServer(server, fixtureServer)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test fixtureServer.
	go server.Serve(li)

	opts := []grpc.DialOption{grpc.WithInsecure()}
	if traceClient {
		opts = append(opts,
			grpc.WithUnaryInterceptor(UnaryClientInterceptor(interceptorOpts...)),
			grpc.WithStreamInterceptor(StreamClientInterceptor(interceptorOpts...)),
		)
	}
	conn, err := grpc.Dial(li.Addr().String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &appsecRig{
		fixtureServer: fixtureServer,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        NewFixtureClient(conn),
	}, err
}

// rig contains all of the servers and connections we'd need for a
// grpc integration test
type appsecRig struct {
	fixtureServer *appsecFixtureServer
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        FixtureClient
}

func (r *appsecRig) Close() {
	r.server.Stop()
	r.conn.Close()
}

type appsecFixtureServer struct {
	s fixtureServer
}

func (s *appsecFixtureServer) StreamPing(stream Fixture_StreamPingServer) (err error) {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get("user-id")
	if err := pappsec.SetUser(ctx, ids[0]); err != nil && err.ShouldBlock() {
		return err
	}
	return s.s.StreamPing(stream)
}
func (s *appsecFixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get("user-id")
	if err := pappsec.SetUser(ctx, ids[0]); err != nil && err.ShouldBlock() {
		return nil, err.GRPCStatus().Err()
	}

	return s.s.Ping(ctx, in)
}
