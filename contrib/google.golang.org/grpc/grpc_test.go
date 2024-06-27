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

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/lists"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
			rig, err := newRig(true, WithServiceName("grpc"), WithRequestTags())
			require.NoError(t, err, "error setting up rig")
			defer rig.Close()
			client := rig.client

			mt := mocktracer.Start()
			defer mt.Stop()

			span, ctx := tracer.StartSpanFromContext(context.Background(), "a", tracer.ServiceName("b"), tracer.ResourceName("c"))

			resp, err := client.Ping(ctx, &FixtureRequest{Name: tt.message})
			span.Finish()
			if tt.error {
				assert.Error(err)
			} else {
				assert.NoError(err)
				assert.Equal(tt.wantMessage, resp.Message)
			}

			spans := mt.FinishedSpans()
			assert.Len(spans, 3)

			var serverSpan, clientSpan, rootSpan mocktracer.Span

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
	runPings := func(t *testing.T, ctx context.Context, client FixtureClient) {
		stream, err := client.StreamPing(ctx)
		assert.NoError(t, err)

		for i := 0; i < 2; i++ {
			err = stream.Send(&FixtureRequest{Name: "pass"})
			assert.NoError(t, err)

			resp, err := stream.Recv()
			assert.NoError(t, err)
			assert.Equal(t, "passed", resp.Message)
		}
		stream.CloseSend()
		// to flush the spans
		stream.Recv()
	}

	checkSpans := func(t *testing.T, rig *rig, spans []mocktracer.Span) {
		var rootSpan mocktracer.Span
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
				wantCode := codes.OK
				if errTag := span.Tag("error"); errTag != nil {
					if err, ok := errTag.(error); ok {
						wantCode = status.Convert(err).Code()
					}
				}
				assert.Equal(t, wantCode.String(), span.Tag(tagCode),
					"expected grpc code to be set in span: %v", span)
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
			case "grpc.server":
				assert.Equal(t, "google.golang.org/grpc", span.Tag(ext.Component),
					" expected component to be grpc-go in span %v", span)
				assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind),
					" expected spankind to be server in span %v, %v", span, span.OperationName())
			case "grpc.message":
				assert.Equal(t, "google.golang.org/grpc", span.Tag(ext.Component),
					" expected component to be grpc-go in span %v", span)
				assert.NotContains(t, span.Tags(), ext.SpanKind,
					" expected no spankind tag to be in span %v", span)
			}

		}
	}

	t.Run("All", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithServiceName("grpc"))
		require.NoError(t, err, "error setting up rig")
		defer rig.Close()

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

		rig, err := newRig(true, WithServiceName("grpc"), WithStreamMessages(false))
		require.NoError(t, err, "error setting up rig")
		defer rig.Close()

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

		rig, err := newRig(true, WithServiceName("grpc"), WithStreamCalls(false))
		require.NoError(t, err, "error setting up rig")
		defer rig.Close()

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
	assertSpan := func(t *testing.T, span, parent mocktracer.Span, operationName, resourceName string) {
		require.NotNil(t, span)
		assert.Nil(t, span.Tag(ext.Error))
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

		rig, err := newRig(true, WithServiceName("grpc"))
		require.NoError(t, err, "error setting up rig")
		defer rig.Close()

		{
			// Unary Ping rpc leading to trace:
			//   root span -> client Ping span -> server Ping span -> child span
			rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")
			client := rig.client
			resp, err := client.Ping(ctx, &FixtureRequest{Name: "child"})
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

		rig, err := newRig(true, WithServiceName("grpc"), WithRequestTags(), WithMetadataTags())
		require.NoError(t, err, "error setting up rig")
		defer rig.Close()
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
			err = stream.SendMsg(&FixtureRequest{Name: "break"})
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

		var rootSpan, clientStreamSpan, serverStreamSpan mocktracer.Span
		var messageSpans []mocktracer.Span
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
					metadataTag := ms.Tag(tagMetadataPrefix + "custom_metadata_key").([]string)
					assert.Len(metadataTag, 1)
					assert.Equal("custom_metadata_value", metadataTag[0])
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

	rig, err := newRig(false, WithServiceName("grpc"))
	require.NoError(t, err, "error setting up rig")
	defer rig.Close()
	client := rig.client

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value")
	resp, err := client.Ping(ctx, &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	assert.Equal("passed", resp.Message)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	s := spans[0]
	assert.Nil(s.Tag(ext.Error))
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
	defer rig.Close()

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value")
	span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
	rig.client.Ping(ctx, &FixtureRequest{Name: "pass"})
	span.Finish()

	md := rig.fixtureServer.lastRequestMetadata.Load().(metadata.MD)
	assert.Equal(t, []string{"test-value"}, md.Get("test-key"),
		"existing metadata should be preserved")

	spans := mt.FinishedSpans()
	s := spans[0]
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-trace-id")
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-parent-id")
	assert.NotContains(t, s.Tags(), tagMetadataPrefix+"x-datadog-sampling-priority")
	assert.Equal(t, []string{"test-value"}, s.Tag(tagMetadataPrefix+"test-key"))
}

func TestStreamSendsErrorCode(t *testing.T) {
	wantCode := codes.InvalidArgument.String()

	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true)
	require.NoError(t, err, "error setting up rig")
	defer rig.Close()

	ctx := context.Background()

	stream, err := rig.client.StreamPing(ctx)
	require.NoError(t, err, "no error should be returned after creating stream client")

	err = stream.Send(&FixtureRequest{Name: "invalid"})
	require.NoError(t, err, "no error should be returned after sending message")

	resp, err := stream.Recv()
	assert.Error(t, err, "should return error")
	assert.Nil(t, resp, "received message should be nil because of error")

	err = stream.CloseSend()
	require.NoError(t, err, "should not return error after closing stream")

	// to flush the spans
	_, _ = stream.Recv()

	containsErrorCode := false
	spans := mt.FinishedSpans()

	// check if at least one span has error code
	for _, s := range spans {
		if s.Tag(tagCode) == wantCode {
			containsErrorCode = true
		}
	}
	assert.True(t, containsErrorCode, "at least one span should contain error code")

	// ensure that last span contains error code also
	gotLastSpanCode := spans[len(spans)-1].Tag(tagCode)
	assert.Equal(t, wantCode, gotLastSpanCode, "last span should contain error code")
}

// fixtureServer a dummy implementation of our grpc fixtureServer.
type fixtureServer struct {
	UnimplementedFixtureServer
	lastRequestMetadata atomic.Value
}

func (s *fixtureServer) StreamPing(stream Fixture_StreamPingServer) (err error) {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		reply, err := s.Ping(stream.Context(), msg)
		if err != nil {
			return err
		}

		err = stream.Send(reply)
		if err != nil {
			return err
		}

		if msg.Name == "break" {
			return nil
		}
	}
}

func (s *fixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		s.lastRequestMetadata.Store(md)
	}
	switch {
	case in.Name == "child":
		span, _ := tracer.StartSpanFromContext(ctx, "child")
		span.Finish()
		return &FixtureReply{Message: "child"}, nil
	case in.Name == "disabled":
		if _, ok := tracer.SpanFromContext(ctx); ok {
			panic("should be disabled")
		}
		return &FixtureReply{Message: "disabled"}, nil
	case in.Name == "invalid":
		return nil, status.Error(codes.InvalidArgument, "invalid")
	case in.Name == "errorDetails":
		s, _ := status.New(codes.Unknown, "unknown").
			WithDetails(&FixtureReply{Message: "a"}, &FixtureReply{Message: "b"})
		return nil, s.Err()
	}
	return &FixtureReply{Message: "passed"}, nil
}

// ensure it's a fixtureServer
var _ FixtureServer = &fixtureServer{}

// rig contains all of the servers and connections we'd need for a
// grpc integration test
type rig struct {
	fixtureServer *fixtureServer
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        FixtureClient
}

func (r *rig) Close() {
	r.server.Stop()
	r.conn.Close()
}

func newRigWithInterceptors(
	serverInterceptors []grpc.ServerOption,
	clientInterceptors []grpc.DialOption,
) (*rig, error) {
	server := grpc.NewServer(serverInterceptors...)
	fixtureSrv := new(fixtureServer)
	RegisterFixtureServer(server, fixtureSrv)

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
		client:        NewFixtureClient(conn),
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

func TestWithErrorCheck(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		for name, tt := range map[string]struct {
			errCheck    func(method string, err error) bool
			message     string
			withError   bool
			wantCode    string
			wantMessage string
		}{
			"Invalid_with_no_error": {
				message: "invalid",
				errCheck: func(method string, err error) bool {
					if err == nil {
						return true
					}

					errCode := status.Code(err)
					if errCode == codes.InvalidArgument && method == "/grpc.Fixture/Ping" {
						return true
					}

					return false
				},
				withError:   false,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
			"Invalid_with_error": {
				message: "invalid",
				errCheck: func(method string, err error) bool {
					if err == nil {
						return true
					}

					errCode := status.Code(err)
					if errCode == codes.InvalidArgument && method == "/some/endpoint" {
						return true
					}

					return false
				},
				withError:   true,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
			"Invalid_with_error_without_errCheck": {
				message:     "invalid",
				errCheck:    nil,
				withError:   true,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
		} {
			t.Run(name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				var ops []Option
				if tt.errCheck != nil {
					ops = append(ops, WithErrorCheck(tt.errCheck))
				}
				rig, err := newRig(true, ops...)
				if err != nil {
					t.Fatalf("error setting up rig: %s", err)
				}

				client := rig.client
				_, err = client.Ping(context.Background(), &FixtureRequest{Name: tt.message})
				assert.Error(t, err)
				assert.Equal(t, tt.wantCode, status.Code(err).String())
				assert.Equal(t, tt.wantMessage, status.Convert(err).Message())

				spans := mt.FinishedSpans()
				assert.Len(t, spans, 2)

				var serverSpan, clientSpan mocktracer.Span

				for _, s := range spans {
					// order of traces in buffer is not garanteed
					switch s.OperationName() {
					case "grpc.server":
						serverSpan = s
					case "grpc.client":
						clientSpan = s
					}
				}

				if tt.withError {
					assert.NotNil(t, clientSpan.Tag(ext.Error))
					assert.NotNil(t, serverSpan.Tag(ext.Error))
				} else {
					assert.Nil(t, clientSpan.Tag(ext.Error))
					assert.Nil(t, serverSpan.Tag(ext.Error))
				}

				rig.Close()
				mt.Reset()
			})
		}
	})

	t.Run("stream", func(t *testing.T) {
		for name, tt := range map[string]struct {
			errCheck    func(method string, err error) bool
			message     string
			withError   bool
			wantCode    string
			wantMessage string
		}{
			"Invalid_with_no_error": {
				message: "invalid",
				errCheck: func(method string, err error) bool {
					if err == nil {
						return true
					}

					errCode := status.Code(err)
					if errCode == codes.InvalidArgument && method == "/grpc.Fixture/StreamPing" {
						return true
					}

					return false
				},
				withError:   false,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
			"Invalid_with_error": {
				message: "invalid",
				errCheck: func(method string, err error) bool {
					if err == nil {
						return true
					}

					errCode := status.Code(err)
					if errCode == codes.InvalidArgument && method == "/some/endpoint" {
						return true
					}

					return false
				},
				withError:   true,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
			"Invalid_with_error_without_errCheck": {
				message:     "invalid",
				errCheck:    nil,
				withError:   true,
				wantCode:    codes.InvalidArgument.String(),
				wantMessage: "invalid",
			},
		} {
			t.Run(name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				var opts []Option
				if tt.errCheck != nil {
					opts = append(opts, WithErrorCheck(tt.errCheck))
				}
				rig, err := newRig(true, opts...)
				if err != nil {
					t.Fatalf("error setting up rig: %s", err)
				}

				ctx, done := context.WithCancel(context.Background())
				client := rig.client
				stream, err := client.StreamPing(ctx)
				assert.NoError(t, err)

				err = stream.Send(&FixtureRequest{Name: tt.message})
				assert.NoError(t, err)

				_, err = stream.Recv()
				assert.Error(t, err)
				assert.Equal(t, tt.wantCode, status.Code(err).String())
				assert.Equal(t, tt.wantMessage, status.Convert(err).Message())

				assert.NoError(t, stream.CloseSend())
				done() // close stream from client side
				rig.Close()

				waitForSpans(mt, 5)

				spans := mt.FinishedSpans()
				assert.Len(t, spans, 5)

				for _, s := range spans {
					if s.Tag(ext.Error) != nil && !tt.withError {
						assert.FailNow(t, "expected no error tag on the span")
					}
				}

				mt.Reset()
			})
		}
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...InterceptorOption) {
		rig, err := newRig(true, opts...)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer rig.Close()

		client := rig.client
		resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
		assert.Nil(t, err)
		assert.Equal(t, "passed", resp.Message)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)

		var serverSpan, clientSpan mocktracer.Span

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

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

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

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})

	t.Run("spanOpts", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.33), WithSpanOptions(tracer.AnalyticsRate(0.23)))
	})
}

func TestIgnoredMethods(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		for _, c := range []struct {
			ignore []string
			exp    int
		}{
			{ignore: []string{}, exp: 2},
			{ignore: []string{"/some/endpoint"}, exp: 2},
			{ignore: []string{"/grpc.Fixture/Ping"}, exp: 1},
			{ignore: []string{"/grpc.Fixture/Ping", "/additional/endpoint"}, exp: 1},
		} {
			rig, err := newRig(true, WithIgnoredMethods(c.ignore...))
			if err != nil {
				t.Fatalf("error setting up rig: %s", err)
			}
			client := rig.client
			resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
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
			{ignore: []string{"/grpc.Fixture/StreamPing"}, exp: 3},
			{ignore: []string{"/grpc.Fixture/StreamPing", "/additional/endpoint"}, exp: 3},
		} {
			rig, err := newRig(true, WithIgnoredMethods(c.ignore...))
			if err != nil {
				t.Fatalf("error setting up rig: %s", err)
			}

			ctx, done := context.WithCancel(context.Background())
			client := rig.client
			stream, err := client.StreamPing(ctx)
			assert.NoError(t, err)

			err = stream.Send(&FixtureRequest{Name: "pass"})
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
			resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
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

			err = stream.Send(&FixtureRequest{Name: "pass"})
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
		{ignore: []string{}, exp: 5},
		{ignore: []string{"test-key"}, exp: 4},
		{ignore: []string{"test-key", "test-key2"}, exp: 3},
	} {
		rig, err := newRig(true, WithMetadataTags(), WithIgnoredMetadata(c.ignore...))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		ctx := context.Background()
		ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value", "test-key2", "test-value2")
		span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
		rig.client.Ping(ctx, &FixtureRequest{Name: "pass"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan mocktracer.Span
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
		resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
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

		err = stream.Send(&FixtureRequest{Name: "pass"})
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
		{key: "val", value: 123},
	} {
		rig, err := newRig(true, WithCustomTag(c.key, c.value))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "x", tracer.ServiceName("y"), tracer.ResourceName("z"))
		rig.client.Ping(ctx, &FixtureRequest{Name: "pass"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan mocktracer.Span
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

func TestServerNamingSchema(t *testing.T) {
	genSpans := getGenSpansFn(false, true)
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		for i := 0; i < 4; i++ {
			assert.Equal(t, "grpc.server", spans[i].OperationName())
		}
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		for i := 0; i < 4; i++ {
			assert.Equal(t, "grpc.server.request", spans[i].OperationName())
		}
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             lists.RepeatString("grpc.server", 4),
		WithDDService:            lists.RepeatString(namingschematest.TestDDService, 4),
		WithDDServiceAndOverride: lists.RepeatString(namingschematest.TestServiceOverride, 4),
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}

func TestClientNamingSchema(t *testing.T) {
	genSpans := getGenSpansFn(true, false)
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		for i := 0; i < 4; i++ {
			assert.Equal(t, "grpc.client", spans[i].OperationName())
		}
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		for i := 0; i < 4; i++ {
			assert.Equal(t, "grpc.client.request", spans[i].OperationName())
		}
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             lists.RepeatString("grpc.client", 4),
		WithDDService:            lists.RepeatString("grpc.client", 4),
		WithDDServiceAndOverride: lists.RepeatString(namingschematest.TestServiceOverride, 4),
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
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
		rig.client.Ping(ctx, &FixtureRequest{Name: "errorDetails"})
		span.Finish()

		spans := mt.FinishedSpans()

		var serverSpan mocktracer.Span
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

func getGenSpansFn(traceClient, traceServer bool) namingschematest.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		// exclude the grpc.message spans as they are not affected by naming schema
		opts = append(opts, WithStreamMessages(false))
		mt := mocktracer.Start()
		defer mt.Stop()

		var serverInterceptors []grpc.ServerOption
		if traceServer {
			serverInterceptors = append(serverInterceptors,
				grpc.UnaryInterceptor(UnaryServerInterceptor(opts...)),
				grpc.StreamInterceptor(StreamServerInterceptor(opts...)),
				grpc.StatsHandler(NewServerStatsHandler(opts...)),
			)
		}
		clientInterceptors := []grpc.DialOption{grpc.WithInsecure()}
		if traceClient {
			clientInterceptors = append(clientInterceptors,
				grpc.WithUnaryInterceptor(UnaryClientInterceptor(opts...)),
				grpc.WithStreamInterceptor(StreamClientInterceptor(opts...)),
				grpc.WithStatsHandler(NewClientStatsHandler(opts...)),
			)
		}
		rig, err := newRigWithInterceptors(serverInterceptors, clientInterceptors)
		require.NoError(t, err)
		defer rig.Close()
		_, err = rig.client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
		require.NoError(t, err)

		stream, err := rig.client.StreamPing(context.Background())
		require.NoError(t, err)
		err = stream.Send(&FixtureRequest{Name: "break"})
		require.NoError(t, err)
		_, err = stream.Recv()
		require.NoError(t, err)
		err = stream.CloseSend()
		require.NoError(t, err)
		// to flush the spans
		_, _ = stream.Recv()

		waitForSpans(mt, 4)
		return mt.FinishedSpans()
	}
}

func BenchmarkUnaryServerInterceptor(b *testing.B) {
	// need to use the real tracer to get representative measurments
	tracer.Start(tracer.WithLogger(log.DiscardLogger{}),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	doNothingOKGRPCHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	}

	unknownErr := status.Error(codes.Unknown, "some unknown error")
	doNothingErrorGRPCHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
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
		for i := 0; i < b.N; i++ {
			interceptor(ctx, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("ok_with_metadata_no_parent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			interceptor(ctxWithMetadataNoParent, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("ok_with_metadata_with_parent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			interceptor(ctxWithMetadataWithParent, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	interceptorWithRate := UnaryServerInterceptor(WithAnalyticsRate(0.5))
	b.Run("ok_no_metadata_with_analytics_rate", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			interceptorWithRate(ctx, "ignoredRequestValue", methodInfo, doNothingOKGRPCHandler)
		}
	})

	b.Run("error_no_metadata", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			interceptor(ctx, "ignoredRequestValue", methodInfo, doNothingErrorGRPCHandler)
		}
	})
	interceptorNoStack := UnaryServerInterceptor(NoDebugStack())
	b.Run("error_no_metadata_no_stack", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
	defer rig.Close()

	// call tracer.Start after integration is initialized, to reproduce the issue
	tracer.Start(tracer.WithHTTPClient(httpClient))
	defer tracer.Stop()

	_, err = rig.client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
	require.NoError(t, err)

	select {
	case <-spansFound:
		return
	}
}
