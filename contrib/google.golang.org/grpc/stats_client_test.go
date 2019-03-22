package grpc

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestClientStatsHandler(t *testing.T) {
	assert := assert.New(t)

	serviceName := "grpc-service"
	statsHandler := NewClientStatsHandler(WithServiceName(serviceName))
	server, err := newClientStatsHandlerTestServer(statsHandler)
	if err != nil {
		t.Fatalf("failed to start test server: %s", err)
	}
	defer server.Close()

	mt := mocktracer.Start()
	defer mt.Stop()

	rootSpan, ctx := tracer.StartSpanFromContext(context.Background(), "a", tracer.ServiceName("b"), tracer.ResourceName("c"))
	_, err = server.client.Ping(ctx, &FixtureRequest{Name: "name"})
	assert.NoError(err)
	rootSpan.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	span := spans[0]
	assert.Equal(rootSpan.Context().SpanID(), span.ParentID())
	assert.NotZero(span.StartTime())
	assert.True(span.FinishTime().After(span.StartTime()))
	assert.Equal("grpc.client", span.OperationName())
	assert.Equal(map[string]interface{}{
		"span.type":     ext.AppTypeRPC,
		"grpc.code":     codes.OK.String(),
		"service.name":  serviceName,
		"resource.name": "/grpc.Fixture/Ping",
		"grpc.method":   "/grpc.Fixture/Ping",
		ext.TargetHost:  "127.0.0.1",
		ext.TargetPort:  server.port,
	}, span.Tags())
}

func newClientStatsHandlerTestServer(statsHandler stats.Handler) (*rig, error) {
	server := grpc.NewServer()
	fixtureServer := new(fixtureServer)
	RegisterFixtureServer(server, fixtureServer)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	go server.Serve(li)

	conn, err := grpc.Dial(li.Addr().String(), grpc.WithInsecure(), grpc.WithStatsHandler(statsHandler))
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
	}, nil
}
