package grpc

import (
	"fmt"
	"net"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestClient(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true)
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
	assert.Equal(clientSpan.Tag("grpc.code"), codes.OK.String())
	assert.Equal(clientSpan.TraceID(), rootSpan.TraceID())
	assert.Equal(serverSpan.Tag(ext.ServiceName), "grpc")
	assert.Equal(serverSpan.Tag(ext.ResourceName), "/grpc.Fixture/Ping")
	assert.Equal(serverSpan.TraceID(), rootSpan.TraceID())
}

func TestChild(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(false)
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
	assert.Equal(s.Tag(ext.SpanType), "go")
	assert.True(s.FinishTime().Sub(s.StartTime()) > 0)
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
		if tracer.SpanFromContext(ctx) != nil {
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

func newRig(traceClient bool) (*rig, error) {
	server := grpc.NewServer(grpc.UnaryInterceptor(UnaryServerInterceptor(WithServiceName("grpc"))))

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
		opts = append(opts, grpc.WithUnaryInterceptor(UnaryClientInterceptor(WithServiceName("grpc"))))
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
