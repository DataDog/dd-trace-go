// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestUnary(t *testing.T) {
	assert := assert.New(t)

	rig, err := newRig(true, WithRequestTags())
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()
	client := rig.client

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
			mt := mocktracer.Start()
			defer mt.Stop()

			span, ctx := tracer.StartSpanFromContext(context.Background(), "a", tracer.ServiceName("b"), tracer.ResourceName("c"))

			resp, err := client.Ping(ctx, &FixtureRequest{Name: tt.message})
			span.Finish()
			if tt.error {
				assert.Error(err)
			} else {
				assert.NoError(err)
				assert.Equal(resp.Message, tt.wantMessage)
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

			assert.Equal(clientSpan.Tag(ext.TargetHost), "127.0.0.1")
			assert.Equal(clientSpan.Tag(ext.TargetPort), rig.port)
			assert.Equal(clientSpan.Tag(tagCode), tt.wantCode.String())
			assert.Equal(clientSpan.TraceID(), rootSpan.TraceID())
			assert.Equal(clientSpan.Tag(tagMethodKind), methodKindUnary)
			assert.Equal(serverSpan.Tag(ext.ServiceName), "grpc")
			assert.Equal(serverSpan.Tag(ext.ResourceName), "/grpc.Fixture/Ping")
			assert.Equal(serverSpan.Tag(tagCode), tt.wantCode.String())
			assert.Equal(serverSpan.TraceID(), rootSpan.TraceID())
			assert.Equal(serverSpan.Tag(tagMethodKind), methodKindUnary)
			assert.Equal(serverSpan.Tag(tagRequest), tt.wantReqTag)
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
			assert.Equal(t, resp.Message, "passed")
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
			}

			switch span.OperationName() {
			case "grpc.client":
				assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost),
					"expected target host tag to be set in span: %v", span)
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
		}
	}

	t.Run("All", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer rig.Close()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 13, 5*time.Second)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 13,
			"expected 4 client messages + 4 server messages + 1 server call + 1 client call + 1 error from empty recv + 1 parent ctx, but got %v",
			len(spans))
		checkSpans(t, rig, spans)
	})

	t.Run("CallsOnly", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithStreamMessages(false))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer rig.Close()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 3, 5*time.Second)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3,
			"expected 1 server call + 1 client call + 1 parent ctx, but got %v",
			len(spans))
		checkSpans(t, rig, spans)
	})

	t.Run("MessagesOnly", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true, WithStreamCalls(false))
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer rig.Close()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "a",
			tracer.ServiceName("b"),
			tracer.ResourceName("c"))

		runPings(t, ctx, rig.client)

		span.Finish()

		waitForSpans(mt, 11, 5*time.Second)

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
		assert.Equal(t, span.Tag(ext.ResourceName), resourceName)
		assert.True(t, span.FinishTime().Sub(span.StartTime()) > 0)

		if parent == nil {
			return
		}
		assert.Equal(t, parent.SpanID(), span.ParentID(), "unexpected parent id")
	}

	t.Run("unary", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRig(true)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
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

		rig, err := newRig(true)
		if err != nil {
			t.Fatalf("error setting up rig: %s", err)
		}
		defer rig.Close()
		client := rig.client

		{
			rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "root")

			// Streaming RPC leading to trace:
			// root -> client stream -> client send message -> server stream
			//  -> server receive message -> server send message
			//  -> client receive message
			ctx, cancel := context.WithCancel(ctx)
			stream, err := client.StreamPing(ctx)
			assert.NoError(err)
			err = stream.SendMsg(&FixtureRequest{Name: "break"})
			assert.NoError(err)
			resp, err := stream.Recv()
			assert.Nil(err)
			assert.Equal(resp.Message, "passed")
			// root span -> client stream Ping span -> send message ping -> child ping
			// TODO
			err = stream.CloseSend()
			assert.NoError(err)
			cancel()

			// Wait until the client stream tracer goroutine gets awoken by the context
			// cancellation and finishes its span
			waitForSpans(mt, 6, time.Second)

			rootSpan.Finish()
		}

		assert.Empty(mt.OpenSpans())
		spans := mt.FinishedSpans()
		//l := len(spans)
		assert.Len(spans, 7)

		// Ping spans

		rootSpan := spans[6]
		clientStreamSpan := spans[5]
		clientStreamSendMsgSpan := spans[4]
		serverStreamSpan := spans[3]
		serverStreamRecvMsgSpan := spans[2]
		serverStreamSendMsgSpan := spans[1]
		clientStreamRecvMsgSpan := spans[0]

		assert.Zero(rootSpan.ParentID())
		assertSpan(t, clientStreamSpan, rootSpan, "grpc.client", "/grpc.Fixture/StreamPing")
		assertSpan(t, clientStreamSendMsgSpan, clientStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
		assertSpan(t, serverStreamSpan, clientStreamSpan, "grpc.server", "/grpc.Fixture/StreamPing")
		assertSpan(t, serverStreamRecvMsgSpan, serverStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
		assertSpan(t, serverStreamSendMsgSpan, serverStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
		assertSpan(t, clientStreamRecvMsgSpan, clientStreamSpan, "grpc.message", "/grpc.Fixture/StreamPing")
	})
}

func TestPass(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(false)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "test-key", "test-value")
	resp, err := client.Ping(ctx, &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	assert.Equal(resp.Message, "passed")

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	s := spans[0]
	assert.Nil(s.Tag(ext.Error))
	assert.Equal(s.OperationName(), "grpc.server")
	assert.Equal(s.Tag(ext.ServiceName), "grpc")
	assert.Equal(s.Tag(ext.ResourceName), "/grpc.Fixture/Ping")
	assert.Equal(s.Tag(ext.SpanType), ext.AppTypeRPC)
	assert.NotContains(s.Tags(), tagRequest)
	assert.NotContains(s.Tags(), tagMetadataPrefix+"test-key")
	assert.True(s.FinishTime().Sub(s.StartTime()) > 0)
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
	assert.Equal(t, s.Tag(tagMetadataPrefix+"test-key"), []string{"test-value"})
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
	assert.Equal(t, gotLastSpanCode, wantCode, "last span should contain error code")
}

// fixtureServer a dummy implementation of our grpc fixtureServer.
type fixtureServer struct {
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

func newRig(traceClient bool, interceptorOpts ...Option) (*rig, error) {
	interceptorOpts = append([]InterceptorOption{WithServiceName("grpc")}, interceptorOpts...)

	server := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryServerInterceptor(interceptorOpts...)),
		grpc.StreamInterceptor(StreamServerInterceptor(interceptorOpts...)),
	)

	fixtureServer := new(fixtureServer)
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
	return &rig{
		fixtureServer: fixtureServer,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        NewFixtureClient(conn),
	}, err
}

// waitForSpans polls the mock tracer until the expected number of spans
// appears
func waitForSpans(mt mocktracer.Tracer, sz int, maxWait time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	for len(mt.FinishedSpans()) < sz {
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(time.Millisecond * 100)
	}
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
		assert.Equal(t, resp.Message, "passed")

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
			assert.Equal(t, resp.Message, "passed")

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
			assert.Equal(t, resp.Message, "passed")

			assert.NoError(t, stream.CloseSend())
			done() // close stream from client side
			rig.Close()

			waitForSpans(mt, c.exp, 5*time.Second)

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
