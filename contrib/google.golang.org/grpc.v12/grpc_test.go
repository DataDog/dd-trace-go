// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"fmt"
	"net"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestClient(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true, true)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()
	client := rig.client

	span, ctx := tracer.StartSpanFromContext(context.Background(), "a", tracer.ServiceName("b"), tracer.ResourceName("c"))
	resp, err := client.Ping(ctx, &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	span.Finish()
	assert.Equal(resp.Message, "passed")

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
	assert.Equal(clientSpan.Tag(tagCode), codes.OK.String())
	assert.Equal(clientSpan.TraceID(), rootSpan.TraceID())
	assert.Equal(clientSpan.Tag(ext.Component), "google.golang.org/grpc.v12")
	assert.Equal(clientSpan.Tag(ext.SpanKind), ext.SpanKindClient)
	assert.Equal("grpc", clientSpan.Tag(ext.RPCSystem))
	assert.Equal("/grpc.Fixture/Ping", clientSpan.Tag(ext.GRPCFullMethod))

	assert.Equal(serverSpan.Tag(ext.ServiceName), "grpc")
	assert.Equal(serverSpan.Tag(ext.ResourceName), "/grpc.Fixture/Ping")
	assert.Equal(serverSpan.TraceID(), rootSpan.TraceID())
	assert.Equal(serverSpan.Tag(ext.Component), "google.golang.org/grpc.v12")
	assert.Equal(serverSpan.Tag(ext.SpanKind), ext.SpanKindServer)
	assert.Equal("grpc", serverSpan.Tag(ext.RPCSystem))
	assert.Equal("/grpc.Fixture/Ping", serverSpan.Tag(ext.GRPCFullMethod))
}

func TestChild(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true, false)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "child"})
	assert.Nil(err)
	assert.Equal(resp.Message, "child")

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var serverSpan, clientSpan mocktracer.Span

	for _, s := range spans {
		// order of traces in buffer is not garanteed
		switch s.OperationName() {
		case "grpc.server":
			serverSpan = s
		case "child":
			clientSpan = s
		}
	}

	assert.NotNil(clientSpan)
	assert.Nil(clientSpan.Tag(ext.Error))
	assert.Equal(clientSpan.Tag(ext.ServiceName), "grpc")
	assert.Equal(clientSpan.Tag(ext.ResourceName), "child")
	assert.True(clientSpan.FinishTime().Sub(clientSpan.StartTime()) > 0)

	assert.NotNil(serverSpan)
	assert.Nil(serverSpan.Tag(ext.Error))
	assert.Equal(serverSpan.Tag(ext.ServiceName), "grpc")
	assert.Equal(serverSpan.Tag(ext.ResourceName), "/grpc.Fixture/Ping")
	assert.True(serverSpan.FinishTime().Sub(serverSpan.StartTime()) > 0)
	assert.Equal(serverSpan.Tag(ext.Component), "google.golang.org/grpc.v12")
	assert.Equal(serverSpan.Tag(ext.SpanKind), ext.SpanKindServer)
	assert.Equal("grpc", serverSpan.Tag(ext.RPCSystem))
	assert.Equal("/grpc.Fixture/Ping", serverSpan.Tag(ext.GRPCFullMethod))
}

func TestPass(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true, false)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
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
	assert.True(s.FinishTime().Sub(s.StartTime()) > 0)
	assert.Equal(s.Tag(ext.Component), "google.golang.org/grpc.v12")
	assert.Equal(s.Tag(ext.SpanKind), ext.SpanKindServer)
}

// fixtureServer a dummy implemenation of our grpc fixtureServer.
type fixtureServer struct{}

func (s *fixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
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
	}
	return &FixtureReply{Message: "passed"}, nil
}

// ensure it's a fixtureServer
var _ FixtureServer = &fixtureServer{}

// rig contains all of the servers and connections we'd need for a
// grpc integration test
type rig struct {
	server   *grpc.Server
	port     string
	listener net.Listener
	conn     *grpc.ClientConn
	client   FixtureClient
}

func (r *rig) Close() {
	r.server.Stop()
	r.conn.Close()
	r.listener.Close()
}

func newRig(traceServer, traceClient bool) (*rig, error) {
	return newRigWithOpts(traceServer, traceClient, WithServiceName("grpc"))
}

func newRigWithOpts(traceServer, traceClient bool, iopts ...InterceptorOption) (*rig, error) {
	var serverOpts []grpc.ServerOption
	if traceServer {
		serverOpts = append(serverOpts, grpc.UnaryInterceptor(UnaryServerInterceptor(iopts...)))
	}
	server := grpc.NewServer(serverOpts...)

	RegisterFixtureServer(server, new(fixtureServer))

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test fixtureServer.
	go server.Serve(li)

	opts := []grpc.DialOption{grpc.WithInsecure()}
	if traceClient {
		opts = append(opts, grpc.WithUnaryInterceptor(UnaryClientInterceptor(iopts...)))
	}
	conn, err := grpc.Dial(li.Addr().String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &rig{
		listener: li,
		port:     port,
		server:   server,
		conn:     conn,
		client:   NewFixtureClient(conn),
	}, err
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...InterceptorOption) {
		rig, err := newRigWithOpts(true, true, opts...)
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

	t.Run("spanOpts", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.33), WithSpanOptions(tracer.AnalyticsRate(0.23)))
	})
}

func TestSpanOpts(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRigWithOpts(true, true, WithSpanOptions(tracer.Tag("foo", "bar")))
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()
	client := rig.client

	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	assert.Equal(resp.Message, "passed")

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	for _, s := range spans {
		assert.Equal(s.Tags()["foo"], "bar")
	}
}

func TestServerNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []InterceptorOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRigWithOpts(true, false, opts...)
		require.NoError(t, err)
		defer rig.Close()
		_, err = rig.client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "grpc.server", spans[0].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "grpc.server.request", spans[0].OperationName())
	}
	ddService := namingschematest.TestDDService
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"grpc.server"},
		WithDDService:            []string{ddService},
		WithDDServiceAndOverride: []string{serviceOverride},
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, "", wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewOpNameTest(genSpans, assertOpV0, assertOpV1))
}

func TestClientNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []InterceptorOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		rig, err := newRigWithOpts(false, true, opts...)
		require.NoError(t, err)
		defer rig.Close()
		_, err = rig.client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "grpc.client", spans[0].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "grpc.client.request", spans[0].OperationName())
	}
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"grpc.client"},
		WithDDService:            []string{"grpc.client"},
		WithDDServiceAndOverride: []string{serviceOverride},
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, "", wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewOpNameTest(genSpans, assertOpV0, assertOpV1))
}
