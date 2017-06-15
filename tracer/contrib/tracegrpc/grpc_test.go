package tracegrpc

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"google.golang.org/grpc"

	context "golang.org/x/net/context"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/stretchr/testify/assert"
)

const (
	debug = false
)

func TestClient(t *testing.T) {
	assert := assert.New(t)

	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	rig, err := newRig(testTracer, true)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()
	client := rig.client

	span := testTracer.NewRootSpan("a", "b", "c")
	ctx := tracer.ContextWithSpan(context.Background(), span)
	resp, err := client.Ping(ctx, &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	span.Finish()
	assert.Equal(resp.Message, "passed")

	assert.Nil(testTracer.ForceFlush())
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 3)

	sspan := spans[0]
	assert.Equal(sspan.Name, "grpc.server")

	cspan := spans[1]
	assert.Equal(cspan.Name, "grpc.client")
	assert.Equal(cspan.GetMeta("grpc.code"), "OK")

	tspan := spans[2]
	assert.Equal(tspan.Name, "a")
	assert.Equal(cspan.TraceID, tspan.TraceID)
	assert.Equal(sspan.TraceID, tspan.TraceID)
}

func TestDisabled(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug
	testTracer.SetEnabled(false)

	rig, err := newRig(testTracer, true)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "disabled"})
	assert.Nil(err)
	assert.Equal(resp.Message, "disabled")
	assert.Nil(testTracer.ForceFlush())
	traces := testTransport.Traces()
	assert.Nil(traces)
}

func TestChild(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	rig, err := newRig(testTracer, false)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "child"})
	assert.Nil(err)
	assert.Equal(resp.Message, "child")
	assert.Nil(testTracer.ForceFlush())
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	s := spans[0]
	assert.Equal(s.Error, int32(0))
	assert.Equal(s.Service, "tracegrpc")
	assert.Equal(s.Resource, "child")
	assert.True(s.Duration > 0)

	s = spans[1]
	assert.Equal(s.Error, int32(0))
	assert.Equal(s.Service, "tracegrpc")
	assert.Equal(s.Resource, "/tracegrpc.Fixture/Ping")
	assert.True(s.Duration > 0)
}

func TestPass(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	rig, err := newRig(testTracer, false)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "pass"})
	assert.Nil(err)
	assert.Equal(resp.Message, "passed")
	assert.Nil(testTracer.ForceFlush())
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal(s.Error, int32(0))
	assert.Equal(s.Name, "grpc.server")
	assert.Equal(s.Service, "tracegrpc")
	assert.Equal(s.Resource, "/tracegrpc.Fixture/Ping")
	assert.Equal(s.Type, "go")
	assert.True(s.Duration > 0)
}

// fixtureServer a dummy implemenation of our grpc fixtureServer.
type fixtureServer struct{}

func newFixtureServer() *fixtureServer {
	return &fixtureServer{}
}

func (s *fixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	switch {
	case in.Name == "child":
		span, ok := tracer.SpanFromContext(ctx)
		if ok {
			t := span.Tracer()
			t.NewChildSpan("child", span).Finish()
		}
		return &FixtureReply{Message: "child"}, nil
	case in.Name == "disabled":
		_, ok := tracer.SpanFromContext(ctx)
		if ok {
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
	listener net.Listener
	conn     *grpc.ClientConn
	client   FixtureClient
}

func (r *rig) Close() {
	r.server.Stop()
	r.conn.Close()
	r.listener.Close()
}

func newRig(t *tracer.Tracer, traceClient bool) (*rig, error) {

	server := grpc.NewServer(grpc.UnaryInterceptor(UnaryServerInterceptor("tracegrpc", t)))

	RegisterFixtureServer(server, newFixtureServer())

	li, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	// start our test fixtureServer.
	go server.Serve(li)

	opts := []grpc.DialOption{
		grpc.WithInsecure(),
	}

	if traceClient {
		opts = append(opts, grpc.WithUnaryInterceptor(UnaryClientInterceptor("tracegrpc", t)))
	}

	conn, err := grpc.Dial(li.Addr().String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}

	r := &rig{
		listener: li,
		server:   server,
		conn:     conn,
		client:   NewFixtureClient(conn),
	}

	return r, err
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	transport := &dummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

// dummyTransport is a transport that just buffers spans and encoding
type dummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *dummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *dummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *dummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *dummyTransport) SetHeader(key, value string) {}
