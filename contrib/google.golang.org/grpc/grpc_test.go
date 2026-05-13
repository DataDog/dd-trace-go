// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

func TestUnary(t *testing.T) {
	assert := assert.New(t)

	for name, tt := range map[string]struct {
		message     string
		error       bool
		wantMessage string
		wantCode    codes.Code
		wantReqTag  string
	}{
		"OK": {
			message:     "pass",
			error:       false,
			wantMessage: "passed",
			wantCode:    codes.OK,
			wantReqTag:  "{\"name\":\"pass\"}",
		},
		"Invalid": {
			message:     "invalid",
			error:       true,
			wantMessage: "",
			wantCode:    codes.InvalidArgument,
			wantReqTag:  "{\"name\":\"invalid\"}",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig, err := newRig(true, WithService("grpc"), WithRequestTags())
			require.NoError(t, err, "error setting up rig")
			defer func() { assert.NoError(rig.Close()) }()
			client := rig.client

			mt := mocktracer.Start()
			defer mt.Stop()

			span, ctx := tracer.StartSpanFromContext(context.Background(), "a", tracer.ServiceName("b"), tracer.ResourceName("c"))

			resp, err := client.Ping(ctx, &fixturepb.FixtureRequest{Name: tt.message})
			span.Finish()
			if tt.error {
				assert.Error(err)
			} else {
				assert.NoError(err)
				assert.Equal(tt.wantMessage, resp.Message)
			}

			spans := mt.FinishedSpans()
			assert.Len(spans, 3)

			var serverSpan, clientSpan, rootSpan *mocktracer.Span

			for _, s := range spans {
				// order of traces in buffer is not garanteed
				switch s.OperationName() {
				case "grpc.server":
					serverSpan = s
				case "grpc.client":
					clientSpan = s
				case "a":
					rootSpan = s
				}
			}

			assert.NotNil(serverSpan)
			assert.NotNil(clientSpan)
			assert.NotNil(rootSpan)

			// this tag always contains the resolved address
			assert.Equal("127.0.0.1", clientSpan.Tag(ext.TargetHost))
			assert.Equal("localhost", clientSpan.Tag(ext.PeerHostname))
			assert.Equal(rig.port, clientSpan.Tag(ext.TargetPort))
			assert.Equal(tt.wantCode.String(), clientSpan.Tag(tagCode))
			assert.Equal(rootSpan.TraceID(), clientSpan.TraceID())
			assert.Equal(methodKindUnary, clientSpan.Tag(tagMethodKind))
			assert.Equal("google.golang.org/grpc", clientSpan.Tag(ext.Component))
			assert.Equal(componentName, clientSpan.Integration())
			assert.Equal(ext.SpanKindClient, clientSpan.Tag(ext.SpanKind))
			assert.Equal("grpc", clientSpan.Tag(ext.RPCSystem))
			assert.Equal("grpc.Fixture", clientSpan.Tag(ext.RPCService))
			assert.Equal("/grpc.Fixture/Ping", clientSpan.Tag(ext.GRPCFullMethod))

			assert.Equal("grpc", serverSpan.Tag(ext.ServiceName))
			assert.Equal("/grpc.Fixture/Ping", serverSpan.Tag(ext.ResourceName))
			assert.Equal(tt.wantCode.String(), serverSpan.Tag(tagCode))
			assert.Equal(rootSpan.TraceID(), serverSpan.TraceID())
			assert.Equal(methodKindUnary, serverSpan.Tag(tagMethodKind))
			assert.Equal(tt.wantReqTag, serverSpan.Tag(tagRequest))
			assert.Equal("google.golang.org/grpc", serverSpan.Tag(ext.Component))
			assert.Equal(ext.SpanKindServer, serverSpan.Tag(ext.SpanKind))
			assert.Equal("grpc", serverSpan.Tag(ext.RPCSystem))
			assert.Equal("grpc.Fixture", serverSpan.Tag(ext.RPCService))
			assert.Equal("/grpc.Fixture/Ping", serverSpan.Tag(ext.GRPCFullMethod))
		})
	}
}

func TestStreaming(t *testing.T) {
	// creates a stream, then sends/recvs two pings, then closes the stream
	runPings := func(t *testing.T, ctx context.Context, client fixturepb.FixtureClient) {
		stream, err := client.StreamPing(ctx)
		assert.NoError(t, err)

		for i := 0; i < 2; i++ {
			err = stream.Send(&fixturepb.FixtureRequest{Name: "pass"})
			assert.NoError(t, err)

			resp, err := stream.Recv()
			assert.NoError(t, err)
			assert.Equal(t, "passed", resp.Message)
		}
		stream.CloseSend()
		// to flush the spans
		stream.Recv()
	}

	checkSpans := func(t *testing.T, rig *rig, spans []*mocktracer.Span) {
		var rootSpan *mocktracer.Span
		for _, span := range spans {
			if span.OperationName() == "a" {
				rootSpan = span
			}
		}
		assert.NotNil(t, rootSpan)
		for _, span := range spans {
			if span != rootSpan {
				assert.Equal(t, rootSpan.TraceID(), span.TraceID(),
					"expected span to to have its trace id set to the root trace id (%d): %v",
					rootSpan.TraceID(), span)
				assert.Equal(t, ext.AppTypeRPC, span.Tag(ext.SpanType),
					"expected span type to be rpc in span: %v",
					span)
				assert.Equal(t, "grpc", span.Tag(ext.ServiceName),
					"expected service name to be grpc in span: %v",
					span)
				assert.Equal(t, "grpc", span.Tag(ext.RPCSystem))
				assert.Equal(t, "/grpc.Fixture/StreamPing", span.Tag(ext.GRPCFullMethod))
			}
			switch span.OperationName() {
			case "grpc.client":
				assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost),
					"expected target host tag to be set in span: %v", span)
				assert.Equal(t, "localhost", span.Tag(ext.PeerHostname))
				assert.Equal(t, rig.port, span.Tag(ext.TargetPort),
					"expected target host port to be set in span: %v", span)
				fallthrough
			case "grpc.server":
				assert.Equal(t, methodKindBidiStream, span.Tag(tagMethodKind),
					"expected tag %s == %s, but found %s.",
					tagMethodKind, methodKindBidiStream, span.Tag(tagMethodKind))
				fallthrough
			case "grpc.message":
				if span.Tag(ext.ErrorMsg) == nil {
					assert.Equal(t, codes.OK.String(), span.Tag(tagCode),
						"expected grpc code to be set in span: %v", span)
				} else {
					assert.NotEqual(t, codes.OK.String(), span.Tag(tagCode),
						"expected grpc code to be set in span: %v", span)
				}
				assert.Equal(t, "/grpc.Fixture/StreamPing", span.Tag(ext.ResourceName),
					"expected resource name to be set in span: %v", span)
				assert.Equal(t, "/grpc.Fixture/StreamPing", span.Tag(tagMethodName),
					"expected grpc method name to be set in span: %v", span)
			}

			switch span.OperationName() { //checks spankind and component without fallthrough
			case "grpc.client":
				assert.Equal(t, "google.golang.org/grpc", span.Tag(ext.Component),
					" expected component to be grpc-go in span %v", span)
				assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind),
					" expected spankind to be client in span %v", span)
				assert.Equal(t, componentName, span.Integration())
			case "grpc.server":
				assert.Equal(t, "google.golang.org/grpc", span.Tag(ext.Component),
					" expected component to be grpc-go in span %v", span)
				assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind),
					" expected spankind to be server in span %v, %v", span, span.OperationName())
				assert.Equal(t, componentName, span.Integration())
			case "grpc.message":
				assert.Equal(t, "google.golang.org/grpc", span.Tag(ext.Component),
					" expected component to be grpc-go in span %v", span)
				assert.NotContains(t, span.Tags(), ext.SpanKind,
					" expected no spankind tag to be in span %v", span)
				assert.Equal(t, componentName, span.Integration())
			}

		}
	}

	t.Run("All", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithService("grpc"))
		require.NoError(t, err, "error setting up rig")
		defer func() { assert.NoError(t, rig.Close()) }()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 13)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 13,
			"expected 4 client messages + 4 server messages + 1 server call + 1 client call + 1 error from empty recv + 1 parent ctx, but got %v",
			len(spans))
		checkSpans(t, rig, spans)
	})

	t.Run("CallsOnly", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithService("grpc"), WithStreamMessages(false))
		require.NoError(t, err, "error setting up rig")
		defer func() { assert.NoError(t, rig.Close()) }()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 3)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3,
			"expected 1 server call + 1 client call + 1 parent ctx, but got %v",
			len(spans))
		checkSpans(t, rig, spans)
	})

	t.Run("MessagesOnly", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithService("grpc"), WithStreamCalls(false))
		require.NoError(t, err, "error setting up rig")
		defer func() { assert.NoError(t, rig.Close()) }()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 11)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 11,
			"expected 4 client messages + 4 server messages + 1 error from empty recv + 1 parent ctx, but got %v",
			len(spans))
		checkSpans(t, rig, spans)
	})
}

func TestSpanTree(t *testing.T) {
	assertSpan := func(t *testing.T, span, parent *mocktracer.Span, operationName, resourceName string) {
		require.NotNil(t, span)
		assert.Zero(t, span.Tag(ext.ErrorMsg))
		assert.Equal(t, operationName, span.OperationName())
		assert.Equal(t, "grpc", span.Tag(ext.ServiceName))
		assert.Equal(t, resourceName, span.Tag(ext.ResourceName))
		assert.True(t, span.FinishTime().Sub(span.StartTime()) >= 0)

		if parent == nil {
			return
		}
		assert.Equal(t, parent.SpanID(), span.ParentID(), "unexpected parent id")
	}

	t.Run("unary", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithService("grpc"))
		require.NoError(t, err, "error setting up rig")
		defer func() { assert.NoError(rig.Close()) }()

		{
			// Unary Ping rpc leading to trace:
			//   root span -> client Ping span -> server Ping span -> child span
			rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")
			client := rig.client
			resp, err := client.Ping(ctx, &fixturepb.FixtureRequest{Name: "child"})
			assert.NoError(err)
			assert.Equal("child", resp.Message)
			rootSpan.Finish()
		}

		assert.Empty(mt.OpenSpans())
		spans := mt.FinishedSpans()
		assert.Len(spans, 4)

		rootSpan := spans[3]
		clientPingSpan := spans[2]
		serverPingSpan := spans[1]
		serverPingChildSpan := spans[0]

		assert.Zero(0, rootSpan.ParentID())
		assertSpan(t, serverPingChildSpan, serverPingSpan, "child", "child")
		assertSpan(t, serverPingSpan, clientPingSpan, "grpc.server", "/grpc.Fixture/Ping")
		assertSpan(t, clientPingSpan, rootSpan, "grpc.client", "/grpc.Fixture/Ping")
	})

	t.Run("stream", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithService("grpc"), WithRequestTags(), WithMetadataTags())
		require.NoError(t, err, "error setting up rig")
		defer func() { assert.NoError(rig.Close()) }()
		client := rig.client

		{
			rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")

			// Streaming RPC leading to trace:
			// root -> client stream -> client send message -> server stream
			//  -> server receive message -> server send message
			//  -> client receive message
			ctx, cancel := context.WithCancel(ctx)
			ctx = metadata.AppendToOutgoingContext(ctx, "custom_metadata_key", "custom_metadata_value")
			stream, err := client.StreamPing(ctx)
			assert.NoError(err)
			err = stream.SendMsg(&fixturepb.FixtureRequest{Name: "break"})
			assert.NoError(err)
			resp, err := stream.Recv()
			assert.Nil(err)
			assert.Equal("passed", resp.Message)
			err = stream.CloseSend()
			assert.NoError(err)
			cancel()

			// Wait until the client stream tracer goroutine gets awoken by the context
			// cancellation and finishes its span
			waitForSpans(mt, 6)

			rootSpan.Finish()
		}

		assert.Empty(mt.OpenSpans())
		spans := mt.FinishedSpans()
		require.Len(t, spans, 7)

		var rootSpan, clientStreamSpan, serverStreamSpan *mocktracer.Span
		var messageSpans []*mocktracer.Span
		for _, s := range spans {
			switch n := s.OperationName(); n {
			case "root":
				rootSpan = s
			case "grpc.client":
				clientStreamSpan = s
			case "grpc.server":
				serverStreamSpan = s
			case "grpc.message":
				messageSpans = append(messageSpans, s)
			}
		}
		require.NotNil(t, rootSpan)
		require.NotNil(t, clientStreamSpan)
		require.NotNil(t, serverStreamSpan)

		assert.Zero(rootSpan.ParentID())
		assertSpan(t, clientStreamSpan, rootSpan, "grpc.client", "/grpc.Fixture/StreamPing")
		assertSpan(t, serverStreamSpan, clientStreamSpan, "grpc.server", "/grpc.Fixture/StreamPing")
		var clientSpans, serverSpans int
		var reqMsgFound bool
		for _, ms := range messageSpans {
			if ms.ParentID() == clientStreamSpan.SpanID() {
				assertSpan(t, ms, clientStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
				clientSpans++
			} else {
				assertSpan(t, ms, serverStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
				serverSpans++
				if !reqMsgFound {
					assert.Equal("{\"name\":\"break\"}", ms.Tag(tagRequest))
					metadataTag := ms.Tag(tagMetadataPrefix + "custom_metadata_key.0")
					assert.Equal("custom_metadata_value", metadataTag)
					reqMsgFound = true
				}
			}
		}
		assert.Equal(2, clientSpans)
		assert.Equal(2, serverSpans)
	})
}

func TestPass(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(false, WithService("grpc"))
	require.NoError(t, err, "error setting up rig")
	defer func() { assert.NoError(rig.Close()) }()
	client := rig.client

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value")
	resp, err := client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
	assert.Nil(err)
	assert.Equal("passed", resp.Message)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	s := spans[0]
	assert.Zero(s.Tag(ext.ErrorMsg))
	assert.Equal("grpc.server", s.OperationName())
	assert.Equal("grpc", s.Tag(ext.ServiceName))
	assert.Equal("/grpc.Fixture/Ping", s.Tag(ext.ResourceName))
	assert.Equal(ext.AppTypeRPC, s.Tag(ext.SpanType))
	assert.NotContains(s.Tags(), tagRequest)
	assert.NotContains(s.Tags(), tagMetadataPrefix+"test-key")
	assert.True(s.FinishTime().Sub(s.StartTime()) >= 0)
	assert.Equal("grpc", s.Tag(ext.RPCSystem))
	assert.Equal("/grpc.Fixture/Ping", s.Tag(ext.GRPCFullMethod))
	assert.Equal(codes.OK.String(), s.Tag(tagCode))
}

func TestPreservesMetadata(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true, WithMetadataTags())
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer func() { assert.NoError(t, rig.Close()) }()

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value")
	span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
	rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
	span.Finish()

	md := rig.fixtureServer.LastRequestMetadata.Load().(metadata.MD)
	assert.Equal(t, []string{"test-value"}, md.Get("test-key"),
		"existing metadata should be preserved")

	spans := mt.FinishedSpans()
	s := spans[0]
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-trace-id")
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-parent-id")
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-sampling-priority")
	assert.Equal(t, "test-value", s.Tag(tagMetadataPrefix+"test-key.0"))
}

func TestStreamSendsErrorCode(t *testing.T) {
	wantCode := codes.InvalidArgument.String()

	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true)
	require.NoError(t, err, "error setting up rig")
	defer func() { assert.NoError(t, rig.Close()) }()

	ctx := context.Background()

	stream, err := rig.client.StreamPing(ctx)
	require.NoError(t, err, "no error should be returned after creating stream client")

	err = stream.Send(&fixturepb.FixtureRequest{Name: "invalid"})
	require.NoError(t, err, "no error should be returned after sending message")

	resp, err := stream.Recv()
	assert.Error(t, err, "should return error")
	assert.Nil(t, resp, "received message should be nil because of error")

	err = stream.CloseSend()
	require.NoError(t, err, "should not return error after closing stream")

	// to flush the spans
	_, _ = stream.Recv()

	spans := mt.FinishedSpans()

	// check if at least one span with spank.kind=server has error code
	var span mocktracer.Span
	for _, s := range spans {
		if s.Tag(tagCode) != wantCode {
			continue
		}
		if s.Tag(ext.SpanKind) != ext.SpanKindServer {
			continue
		}
		span = *s
	}
	assert.NotNilf(t, span, "at least one span should contain error code, the spans were:\n%v", spans)
}

// rig contains all of the servers and connections we'd need for a
// grpc integration test
type rig struct {
	fixtureServer *fixturepb.FixtureSrv
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        fixturepb.FixtureClient
}

func (r *rig) Close() error {
	defer r.server.GracefulStop()
	return r.conn.Close()
}

func newRigWithInterceptors(
	serverInterceptors []grpc.ServerOption,
	clientInterceptors []grpc.DialOption,
) (*rig, error) {
	server := grpc.NewServer(serverInterceptors...)
	fixtureSrv := fixturepb.NewFixtureServer()
	fixturepb.RegisterFixtureServer(server, fixtureSrv)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test fixtureServer.
	go server.Serve(li)

	conn, err := grpc.Dial("localhost:"+port, clientInterceptors...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &rig{
		fixtureServer: fixtureSrv,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        fixturepb.NewFixtureClient(conn),
	}, err
}

func newRig(traceClient bool, opts ...Option) (*rig, error) {
	serverInterceptors := []grpc.ServerOption{
		grpc.UnaryInterceptor(UnaryServerInterceptor(opts...)),
		grpc.StreamInterceptor(StreamServerInterceptor(opts...)),
	}
	clientInterceptors := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	if traceClient {
		clientInterceptors = append(clientInterceptors,
			grpc.WithUnaryInterceptor(UnaryClientInterceptor(opts...)),
			grpc.WithStreamInterceptor(StreamClientInterceptor(opts...)),
		)
	}
	return newRigWithInterceptors(serverInterceptors, clientInterceptors)
}

// waitForSpans polls the mock tracer until the expected number of spans
// appears
func waitForSpans(mt mocktracer.Tracer, sz int) {
	for len(mt.FinishedSpans()) < sz {
		time.Sleep(time.Millisecond * 100)
	}
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		rig, err := newRig(true, opts...)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer func() { assert.NoError(t, rig.Close()) }()

		client := rig.client
		resp, err := client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "pass"})
		assert.Nil(t, err)
		assert.Equal(t, "passed", resp.Message)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)

		var serverSpan, clientSpan *mocktracer.Span

		for _, s := range spans {
			// order of traces in buffer is not garanteed
			switch s.OperationName() {
			case "grpc.server":
				serverSpan = s
			case "grpc.client":
				clientSpan = s
			}
		}

		assert.Equal(t, rate, clientSpan.Tag(ext.EventSampleRate))
		assert.Equal(t, rate, serverSpan.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})

	t.Run("spanOpts", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.33), WithSpanOptions(tracer.AnalyticsRate(0.23)))
	})
}

func TestUntracedMethods(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		for _, c := range []struct {
			ignore []string
			exp    int
		}{
			{ignore: []string{}, exp: 2},
			{ignore: []string{"/some/endpoint"}, exp: 2},
			{ignore: []string{"/grpc.Fixture/Ping"}, exp: 0},
			{ignore: []string{"/grpc.Fixture/Ping", "/additional/endpoint"}, exp: 0},
		} {
			rig, err := newRig(true, WithUntracedMethods(c.ignore...))
			if err != nil {
				t.Fatalf("error setting up rig: %s", err)
			}
			client := rig.client
			resp, err := client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "pass"})
			assert.Nil(t, err)
			assert.Equal(t, "passed", resp.Message)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, c.exp)
			rig.Close()
			mt.Reset()
		}
	})

	t.Run("stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		for _, c := range []struct {
			ignore []string
			exp    int
		}{
			// client span: 1 send + 1 recv(OK) + 1 stream finish (OK)
			// server span: 1 send + 2 recv(OK + EOF) + 1 stream finish(EOF)
			{ignore: []string{}, exp: 7},
			{ignore: []string{"/some/endpoint"}, exp: 7},
			{ignore: []string{"/grpc.Fixture/StreamPing"}, exp: 0},
			{ignore: []string{"/grpc.Fixture/StreamPing", "/additional/endpoint"}, exp: 0},
		} {
			rig, err := newRig(true, WithUntracedMethods(c.ignore...))
			if err != nil {
				t.Fatalf("error setting up rig: %s", err)
			}

			ctx, done := context.WithCancel(context.Background())
			client := rig.client
			stream, err := client.StreamPing(ctx)
			assert.NoError(t, err)

			err = stream.Send(&fixturepb.FixtureRequest{Name: "pass"})
			assert.NoError(t, err)

			resp, err := stream.Recv()
			assert.NoError(t, err)
			assert.Equal(t, "passed", resp.Message)

			assert.NoError(t, stream.CloseSend())
			done() // close stream from client side
			rig.Close()

			waitForSpans(mt, c.exp)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, c.exp)
			mt.Reset()
		}
	})
}

func TestIgnoredMetadata(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, c := range []struct {
		ignore []string
		exp    int
	}{
		{ignore: []string{}, exp: 8},
		{ignore: []string{"test-key"}, exp: 7},
		{ignore: []string{"test-key", "test-key2"}, exp: 6},
	} {
		rig, err := newRig(true, WithMetadataTags(), WithIgnoredMetadata(c.ignore...))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		ctx := context.Background()
		ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value", "test-key2", "test-value2")
		span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
		rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan *mocktracer.Span
		for _, s := range spans {
			switch s.OperationName() {
			case "grpc.server":
				serverSpan = s
			}
		}

		var cnt int
		for k := range serverSpan.Tags() {
			if strings.HasPrefix(k, tagMetadataPrefix) {
				cnt++
			}
		}
		assert.Equal(t, c.exp, cnt)
		rig.Close()
		mt.Reset()
	}
}

func TestSpanOpts(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		rig, err := newRig(true, WithSpanOptions(tracer.Tag("foo", "bar")))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		client := rig.client
		resp, err := client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "pass"})
		assert.Nil(t, err)
		assert.Equal(t, "passed", resp.Message)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)

		for _, span := range spans {
			assert.Equal(t, "bar", span.Tags()["foo"])
		}
		rig.Close()
		mt.Reset()
	})

	t.Run("stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		rig, err := newRig(true, WithSpanOptions(tracer.Tag("foo", "bar")))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}

		ctx, done := context.WithCancel(context.Background())
		client := rig.client
		stream, err := client.StreamPing(ctx)
		assert.NoError(t, err)

		err = stream.Send(&fixturepb.FixtureRequest{Name: "pass"})
		assert.NoError(t, err)

		resp, err := stream.Recv()
		assert.NoError(t, err)
		assert.Equal(t, "passed", resp.Message)

		assert.NoError(t, stream.CloseSend())
		done() // close stream from client side
		rig.Close()

		waitForSpans(mt, 7)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 7)
		for _, span := range spans {
			assert.Equal(t, "bar", span.Tags()["foo"])
		}
		mt.Reset()
	})
}

func TestCustomTag(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, c := range []struct {
		key   string
		value interface{}
	}{
		{key: "foo", value: "bar"},
		{key: "val", value: float64(123)},
	} {
		rig, err := newRig(true, WithCustomTag(c.key, c.value))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
		rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan *mocktracer.Span
		for _, s := range spans {
			switch s.OperationName() {
			case "grpc.server":
				serverSpan = s
			}
		}

		assert.NotNil(t, serverSpan)
		assert.Equal(t, c.value, serverSpan.Tag(c.key))
		rig.Close()
		mt.Reset()
	}
}

func TestWithErrorDetailTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, c := range []struct {
		opts     []Option
		details0 interface{}
		details1 interface{}
		details2 interface{}
	}{
		{opts: []Option{WithErrorDetailTags()}, details0: "message:\"a\"", details1: "message:\"b\"", details2: nil},
		{opts: []Option{}, details0: nil, details1: nil, details2: nil},
	} {
		rig, err := newRig(true, c.opts...)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
		rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "errorDetails"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan *mocktracer.Span
		for _, s := range spans {
			switch s.OperationName() {
			case "grpc.server":
				serverSpan = s
			}
		}

		assert.NotNil(t, serverSpan)
		assert.Equal(t, c.details0, serverSpan.Tag("grpc.status_details._0"))
		assert.Equal(t, c.details1, serverSpan.Tag("grpc.status_details._1"))
		assert.Equal(t, c.details2, serverSpan.Tag("grpc.status_details._2"))
		rig.Close()
		mt.Reset()
	}
}

func BenchmarkUnaryServerInterceptor(b *testing.B) {
	// need to use the real tracer to get representative measurments
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	doNothingOKGRPCHandler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, nil
	}

	unknownErr := status.Error(codes.Unknown, "some unknown error")
	doNothingErrorGRPCHandler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, unknownErr
	}

	// Add gRPC metadata to ctx to get resonably accurate performance numbers. From a production
	// application, there can be quite a few key/value pairs. A number of these are added by
	// gRPC itself, and others by Datadog tracing
	md := metadata.Pairs(
		":authority", "example-service-name.example.com:12345",
		"content-type", "application/grpc",
		"user-agent", "grpc-go/1.32.0",
		"x-datadog-sampling-priority", "1",
	)
	mdWithParent := metadata.Join(md, metadata.Pairs(
		"x-datadog-trace-id", "9219028207762307503",
		"x-datadog-parent-id", "7525005002014855056",
	))
	ctx := context.Background()
	ctxWithMetadataNoParent := metadata.NewIncomingContext(ctx, md)
	ctxWithMetadataWithParent := metadata.NewIncomingContext(ctx, mdWithParent)

	methodInfo := &grpc.UnaryServerInfo{FullMethod: "/package.MyService/ExampleMethod"}
	interceptor := UnaryServerInterceptor()
	b.Run("ok_no_metadata", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptor(ctx, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("ok_with_metadata_no_parent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptor(ctxWithMetadataNoParent, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("ok_with_metadata_with_parent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptor(ctxWithMetadataWithParent, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	interceptorWithRate := UnaryServerInterceptor(WithAnalyticsRate(0.5))
	b.Run("ok_no_metadata_with_analytics_rate", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptorWithRate(ctx, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("error_no_metadata", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptor(ctx, "ignoredRequestValue", methodInfo, doNothingErrorGRPCHandler)
		}
	})
	interceptorNoStack := UnaryServerInterceptor(NoDebugStack())
	b.Run("error_no_metadata_no_stack", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			interceptorNoStack(ctx, "ignoredRequestValue", methodInfo, doNothingErrorGRPCHandler)
		}
	})
}

type roundTripper struct {
	assertSpanFromRequest func(r *http.Request)
}

func (rt *roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.assertSpanFromRequest(r)
	return http.DefaultTransport.RoundTrip(r)
}

func TestIssue2050(t *testing.T) {
	// https://github.com/DataDog/dd-trace-go/issues/2050
	t.Setenv("DD_SERVICE", "some-dd-service")

	spansFound := make(chan bool, 1)

	httpClient := &http.Client{
		Transport: &roundTripper{
			assertSpanFromRequest: func(r *http.Request) {
				if r.URL.Path != "/v0.4/traces" {
					return
				}
				req := r.Clone(context.Background())
				defer req.Body.Close()

				buf, err := io.ReadAll(req.Body)
				require.NoError(t, err)

				var payload bytes.Buffer
				_, err = msgp.UnmarshalAsJSON(&payload, buf)
				require.NoError(t, err)

				var trace [][]map[string]interface{}
				err = json.Unmarshal(payload.Bytes(), &trace)
				require.NoError(t, err)

				if len(trace) == 0 {
					return
				}
				require.Len(t, trace, 2)
				s0 := trace[0][0]
				s1 := trace[1][0]

				assert.Equal(t, "server", s0["meta"].(map[string]interface{})["span.kind"])
				assert.Equal(t, "some-dd-service", s0["service"])

				assert.Equal(t, "client", s1["meta"].(map[string]interface{})["span.kind"])
				assert.Equal(t, "grpc.client", s1["service"])
				close(spansFound)
			},
		},
	}
	serverInterceptors := []grpc.ServerOption{
		grpc.UnaryInterceptor(UnaryServerInterceptor()),
		grpc.StreamInterceptor(StreamServerInterceptor()),
	}
	clientInterceptors := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(StreamClientInterceptor()),
	}
	rig, err := newRigWithInterceptors(serverInterceptors, clientInterceptors)
	require.NoError(t, err)
	defer func() { assert.NoError(t, rig.Close()) }()

	// call tracer.Start after integration is initialized, to reproduce the issue
	tracer.Start(tracer.WithHTTPClient(httpClient), tracer.WithLogger(testutils.DiscardLogger()))
	defer tracer.Stop()

	_, err = rig.client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "pass"})
	require.NoError(t, err)

	select {
	case <-spansFound:
		return
	}
}

// retryStreamSrv returns codes.Unavailable on the first StreamPing call and
// sends a single successful reply on all subsequent calls.
type retryStreamSrv struct {
	fixturepb.UnimplementedFixtureServer
	attempts atomic.Int32
}

func (s *retryStreamSrv) StreamPing(stream fixturepb.Fixture_StreamPingServer) error {
	if s.attempts.Add(1) == 1 {
		return status.Error(codes.Unavailable, "try again")
	}
	return stream.Send(&fixturepb.FixtureReply{Message: "passed"})
}

// TestStreamClientRetryPolicyRespected verifies that StreamClientInterceptor
// does not disable gRPC client-side retries. Calling stream.Context() before
// Header or RecvMsg commits the attempt and prevents retries; the interceptor
// currently does this at stream creation time.
func TestStreamClientRetryPolicyRespected(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := &retryStreamSrv{}

	const serviceConfig = `{"methodConfig":[{"name":[{"service":"grpc.Fixture"}],"retryPolicy":{"maxAttempts":3,"initialBackoff":"0.01s","maxBackoff":"0.1s","backoffMultiplier":2.0,"retryableStatusCodes":["UNAVAILABLE"]}}]}`

	server := grpc.NewServer(
		grpc.StreamInterceptor(StreamServerInterceptor(WithService("grpc"))),
	)
	fixturepb.RegisterFixtureServer(server, srv)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, _ := net.SplitHostPort(li.Addr().String())
	go server.Serve(li)
	defer server.GracefulStop()

	conn, err := grpc.NewClient("localhost:"+port,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithStreamInterceptor(StreamClientInterceptor(WithService("grpc"))),
	)
	require.NoError(t, err)
	defer conn.Close()

	stream, err := fixturepb.NewFixtureClient(conn).StreamPing(context.Background())
	require.NoError(t, err)

	// Calling Recv() without a prior Send() does not commit the bidi-stream
	// attempt, so the gRPC runtime can retry it. Our interceptor's premature
	// stream.Context() call is what commits and blocks the retry today.
	resp, err := stream.Recv()
	require.NoError(t, err,
		"expected retry to succeed; if retries are disabled by a premature "+
			"stream.Context() call the first Unavailable error propagates here")
	assert.Equal(t, "passed", resp.Message)
	assert.Equal(t, int32(2), srv.attempts.Load(),
		"server should have been called twice: first attempt fails, retry succeeds")

	// Drain to EOF so the gRPC runtime cancels the stream context and the
	// interceptor's cleanup goroutine can finish the call span.
	_, _ = stream.Recv()

	// Spans:
	//   - 1 grpc.client (interceptor is invoked once; retries are transparent)
	//   - 2 grpc.server (one per server attempt)
	//   - 3 grpc.message: 1 server SendMsg on the successful attempt and 2
	//     client RecvMsg spans (one for "passed", one for the EOF drain).
	waitForSpans(mt, 6)
	var clientSpans, serverSpans, messageSpans int
	for _, s := range mt.FinishedSpans() {
		switch s.OperationName() {
		case "grpc.client":
			clientSpans++
		case "grpc.server":
			serverSpans++
		case "grpc.message":
			messageSpans++
		}
	}
	assert.Equal(t, 1, clientSpans, "interceptor is called once; retries are transparent")
	assert.Equal(t, 2, serverSpans, "one span per server attempt (failed + retried)")
	assert.Equal(t, 3, messageSpans, "1 server SendMsg + 2 client RecvMsg (response + EOF drain)")
}

// TestStreamRetryable verifies that the StreamClientInterceptor does not
// disable gRPC's retry policy. The server's tap handler rejects every attempt
// with codes.Unavailable, and a retry policy is configured so that gRPC will
// retry up to 3 times. Both the unary and stream RPCs must be retried 3 times.
//
// The test also asserts on the per-attempt spans emitted by the stats handler:
// transparent retries are invisible to interceptors but visible to stats
// handlers, so one parent span (from the interceptor) and one child span per
// attempt (from the stats handler) are expected.
func TestStreamRetryable(t *testing.T) {
	serviceConfig := `{
		"methodConfig": [{
             "name": [
               {"service": "grpc.Fixture", "method": "Ping"},
               {"service": "grpc.Fixture", "method": "StreamPing"}
             ],
            "retryPolicy": {
              "maxAttempts": 3,
              "initialBackoff": "0.000000001s",
              "maxBackoff": "0.000000001s",
              "backoffMultiplier": 2,
              "retryableStatusCodes": [
                "UNAVAILABLE"
              ]
            }
		}]
    }`
	mt := mocktracer.Start()
	defer mt.Stop()

	var unaryAttempts atomic.Int64
	var streamAttempts atomic.Int64
	serverOpts := []grpc.ServerOption{
		grpc.UnaryInterceptor(UnaryServerInterceptor()),
		grpc.StreamInterceptor(StreamServerInterceptor()),
		grpc.InTapHandle(func(ctx context.Context, info *tap.Info) (context.Context, error) {
			switch info.FullMethodName {
			case fixturepb.Fixture_Ping_FullMethodName:
				unaryAttempts.Add(1)
			case fixturepb.Fixture_StreamPing_FullMethodName:
				streamAttempts.Add(1)
			}
			return ctx, status.Error(codes.Unavailable, "not now")
		}),
	}
	dialOpts := []grpc.DialOption{
		grpc.WithDisableServiceConfig(),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(StreamClientInterceptor()),
		// Transparent retries are invisible to interceptors, but not stats handlers.
		grpc.WithStatsHandler(NewClientStatsHandler()),
	}

	rig, err := newRigWithInterceptors(serverOpts, dialOpts)
	require.NoError(t, err, "error setting up rig")
	t.Cleanup(func() { _ = rig.Close() })

	ctx := t.Context()
	req := &fixturepb.FixtureRequest{Name: "pass"}

	_, err = rig.client.Ping(ctx, req)
	require.Error(t, err, "unary should return error")
	require.EqualValues(t, 3, unaryAttempts.Load(), "unary should attempt more than once")

	stream, err := rig.client.StreamPing(ctx)
	require.NoError(t, err, "no error should be returned after creating stream client")

	// Skip stream.Send() to avoid marking the request as ineligible for retry.
	_, err = stream.Recv()
	require.Error(t, err, "stream should return error")
	require.EqualValues(t, 3, streamAttempts.Load(), "stream should attempt more than once")
	require.NoError(t, rig.Close())

	// One parent client span per RPC (unary, stream) and one child span per
	// attempt (from the stats handler).
	var parentSpans, childSpans []*mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.Tag(ext.SpanKind) != ext.SpanKindClient {
			continue
		}
		if s.ParentID() == 0 {
			parentSpans = append(parentSpans, s)
		} else {
			childSpans = append(childSpans, s)
		}
	}

	wantChildren := int(unaryAttempts.Load() + streamAttempts.Load())
	require.Len(t, parentSpans, 2, "should have a span for initiated request")
	require.Len(t, childSpans, wantChildren, "should have a span for each attempt")
}

// TestStreamMessageSpanParentsWhenCallsDisabled verifies that when
// WithStreamCalls(false) is set (no grpc.client or grpc.server call spans),
// all grpc.message spans have the user's root span as their parent — not a
// now-absent call span.
func TestStreamMessageSpanParentsWhenCallsDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true, WithService("grpc"), WithStreamCalls(false))
	require.NoError(t, err)
	defer func() { assert.NoError(t, rig.Close()) }()

	rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")
	ctx, cancel := context.WithCancel(ctx)

	stream, err := rig.client.StreamPing(ctx)
	require.NoError(t, err)

	err = stream.Send(&fixturepb.FixtureRequest{Name: "pass"})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "passed", resp.Message)

	stream.CloseSend()
	cancel()
	rootSpan.Finish()

	// root + message spans: client send, client recv, server recv(pass),
	// server send, server recv(EOF or ctx cancel)
	waitForSpans(mt, 6)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 6)

	var root *mocktracer.Span
	for _, s := range spans {
		if s.OperationName() == "root" {
			root = s
		}
	}
	require.NotNil(t, root)

	for _, s := range spans {
		if s.OperationName() != "grpc.message" {
			continue
		}
		assert.Equal(t, root.SpanID(), s.ParentID(),
			"grpc.message span should have root span as parent when call spans are disabled, got span: %v", s)
	}
}

// TestStreamClientCallSpan verifies the grpc.client and grpc.server call span
// behavior (tag set, finish status, no leaked open spans) across the different
// ways a streaming RPC can end.
func TestStreamClientCallSpan(t *testing.T) {
	cases := []struct {
		name           string // subtest name
		request        string // request Name to send
		drainEOF       bool   // whether to CloseSend + drain after the first Recv
		wantRecvErr    bool   // whether the first stream.Recv() should return an error
		wantServerCode string // expected tagCode on grpc.server
		wantServerErr  bool   // whether grpc.server should carry an error tag
	}{
		{
			name:           "FinishOnEOF",
			request:        "break",
			drainEOF:       true,
			wantServerCode: codes.OK.String(),
		},
		{
			name:           "FinishOnServerError",
			request:        "invalid",
			wantRecvErr:    true,
			wantServerCode: codes.InvalidArgument.String(),
			wantServerErr:  true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rig, err := newRig(true, WithService("grpc"), WithStreamMessages(false))
			require.NoError(t, err)
			defer func() { assert.NoError(t, rig.Close()) }()

			rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")
			stream, err := rig.client.StreamPing(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: tt.request}))

			_, err = stream.Recv()
			if tt.wantRecvErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.drainEOF {
				require.NoError(t, stream.CloseSend())
				_, _ = stream.Recv()
			}
			rootSpan.Finish()

			waitForSpans(mt, 3) // root + grpc.client + grpc.server
			assert.Empty(t, mt.OpenSpans(), "no spans should be left open")

			spans := mt.FinishedSpans()
			require.Len(t, spans, 3)

			var clientSpan, serverSpan *mocktracer.Span
			for _, s := range spans {
				switch s.OperationName() {
				case "grpc.client":
					clientSpan = s
				case "grpc.server":
					serverSpan = s
				}
			}
			require.NotNil(t, clientSpan, "grpc.client span must be finished")
			require.NotNil(t, serverSpan, "grpc.server span must be finished")

			// Client call span: full tag set, plus finishes with codes.OK
			// regardless of the server-side outcome (the gRPC stream context
			// reports context.Canceled when the stream ends, which
			// finishWithError treats as a non-error).
			assert.Equal(t, "127.0.0.1", clientSpan.Tag(ext.TargetHost))
			assert.Equal(t, "localhost", clientSpan.Tag(ext.PeerHostname))
			assert.Equal(t, rig.port, clientSpan.Tag(ext.TargetPort))
			assert.Equal(t, codes.OK.String(), clientSpan.Tag(tagCode))
			assert.Nil(t, clientSpan.Tag(ext.ErrorMsg))
			assert.Equal(t, methodKindBidiStream, clientSpan.Tag(tagMethodKind))
			assert.Equal(t, "google.golang.org/grpc", clientSpan.Tag(ext.Component))
			assert.Equal(t, componentName, clientSpan.Integration())
			assert.Equal(t, ext.SpanKindClient, clientSpan.Tag(ext.SpanKind))
			assert.Equal(t, "grpc", clientSpan.Tag(ext.RPCSystem))
			assert.Equal(t, "grpc.Fixture", clientSpan.Tag(ext.RPCService))
			assert.Equal(t, "/grpc.Fixture/StreamPing", clientSpan.Tag(ext.GRPCFullMethod))
			assert.Equal(t, "/grpc.Fixture/StreamPing", clientSpan.Tag(ext.ResourceName))
			assert.Equal(t, ext.AppTypeRPC, clientSpan.Tag(ext.SpanType))
			assert.True(t, clientSpan.FinishTime().After(clientSpan.StartTime()))

			// Server call span: full tag set, plus code/error reflecting the
			// handler's outcome.
			assert.Equal(t, "grpc", serverSpan.Tag(ext.ServiceName))
			assert.Equal(t, "/grpc.Fixture/StreamPing", serverSpan.Tag(ext.ResourceName))
			assert.Equal(t, tt.wantServerCode, serverSpan.Tag(tagCode))
			if tt.wantServerErr {
				assert.NotNil(t, serverSpan.Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, serverSpan.Tag(ext.ErrorMsg))
			}
			assert.Equal(t, methodKindBidiStream, serverSpan.Tag(tagMethodKind))
			assert.Equal(t, "google.golang.org/grpc", serverSpan.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindServer, serverSpan.Tag(ext.SpanKind))
			assert.Equal(t, "grpc", serverSpan.Tag(ext.RPCSystem))
			assert.Equal(t, "grpc.Fixture", serverSpan.Tag(ext.RPCService))
			assert.Equal(t, "/grpc.Fixture/StreamPing", serverSpan.Tag(ext.GRPCFullMethod))
		})
	}
}

// slowStreamSrv is a FixtureServer that sleeps for a configurable duration
// before replying to each stream message. Used to test that grpc.message
// spans cover the actual I/O wait time.
type slowStreamSrv struct {
	fixturepb.UnimplementedFixtureServer
	delay time.Duration
}

func (s *slowStreamSrv) StreamPing(stream fixturepb.Fixture_StreamPingServer) error {
	_, err := stream.Recv()
	if err != nil {
		return err
	}
	time.Sleep(s.delay)
	return stream.Send(&fixturepb.FixtureReply{Message: "slow"})
}

// newRigWithHandler creates a test rig using a custom FixtureServer
// implementation instead of the default one.
func newRigWithHandler(handler fixturepb.FixtureServer, opts ...Option) (*rig, error) {
	serverOpts := []grpc.ServerOption{
		grpc.StreamInterceptor(StreamServerInterceptor(opts...)),
	}
	server := grpc.NewServer(serverOpts...)
	fixturepb.RegisterFixtureServer(server, handler)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	go server.Serve(li)

	conn, err := grpc.NewClient("localhost:"+port,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStreamInterceptor(StreamClientInterceptor(opts...)),
	)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &rig{
		listener: li,
		port:     port,
		server:   server,
		conn:     conn,
		client:   fixturepb.NewFixtureClient(conn),
	}, nil
}

// TestStreamRecvMsgSpanDuration verifies that grpc.message spans for RecvMsg
// cover the actual time spent waiting for a server response. A span that is
// started and finished before the underlying RecvMsg returns would not capture
// the real I/O duration.
func TestStreamRecvMsgSpanDuration(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	const delay = 100 * time.Millisecond

	rig, err := newRigWithHandler(&slowStreamSrv{delay: delay}, WithService("grpc"))
	require.NoError(t, err)
	defer func() { assert.NoError(t, rig.Close()) }()

	stream, err := rig.client.StreamPing(context.Background())
	require.NoError(t, err)

	require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "slow"}))

	_, err = stream.Recv()
	require.NoError(t, err)

	// Drain to EOF so the client call span is finished by the cleanup
	// goroutine watching the stream context.
	require.NoError(t, stream.CloseSend())
	_, _ = stream.Recv()

	// Spans:
	//   - 1 grpc.client (call)
	//   - 1 grpc.server (call)
	//   - 3 client grpc.message: Send + Recv("slow") + Recv(EOF)
	//   - 2 server grpc.message: Recv + Send
	waitForSpans(mt, 7)

	// The client's RecvMsg("slow") span must cover at least the server-imposed
	// delay because the client is blocked waiting for the server to respond.
	var slowSpan *mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.OperationName() != "grpc.message" {
			continue
		}
		if s.FinishTime().Sub(s.StartTime()) >= delay {
			slowSpan = s
		}
	}
	require.NotNil(t, slowSpan,
		"expected a grpc.message span with duration >= %s covering the server delay; spans: %v", delay, mt.FinishedSpans())
	assert.GreaterOrEqual(t, slowSpan.FinishTime().Sub(slowSpan.StartTime()), delay)
}
