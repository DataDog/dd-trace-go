package tracegrpc

import (
	"fmt"
	"net"
	"testing"

	"google.golang.org/grpc"

	context "golang.org/x/net/context"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/stretchr/testify/assert"
)

func TestServer(t *testing.T) {
	assert := assert.New(t)

	testTransport := &dummyTransport{}
	testTracer := getTestTracer(testTransport)
	testTracer.DebugLoggingEnabled = true

	rig, err := newRig(testTracer)
	if err != nil {
		t.Fatalf("error setting up rig: %s", err)
	}
	defer rig.Close()

	client := rig.client
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "foo"})
	assert.Nil(err)
	assert.Equal(resp.Message, "Fixture foo")
	assert.Nil(testTracer.Flush())
	spans := testTransport.Spans()
	assert.NotNil(nil)
	assert.Len(spans, 1)
}

// fixtureServer a dummy implemenation of our grpc fixtureServer.
type fixtureServer struct{}

func newFixtureServer() *fixtureServer {
	return &fixtureServer{}
}
func (s *fixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	return &FixtureReply{Message: "Fixture " + in.Name}, nil
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
	r.listener.Close()
	r.conn.Close()
}

func newRig(t *tracer.Tracer) (*rig, error) {

	ti := Interceptor(t)

	server := grpc.NewServer(grpc.UnaryInterceptor(ti))

	RegisterFixtureServer(server, newFixtureServer())

	li, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	// start our test fixtureServer.
	go server.Serve(li)

	conn, err := grpc.Dial(li.Addr().String(), grpc.WithInsecure())
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

func getTestTracer(transport tracer.Transport) *tracer.Tracer {
	testTracer := tracer.NewTracerTransport(transport)
	return testTracer
}

// dummyTransport is a transport that just buffers spans.
type dummyTransport struct {
	spans []*tracer.Span
}

func (d *dummyTransport) Send(s []*tracer.Span) error {
	d.spans = append(d.spans, s...)
	return nil
}

func (d *dummyTransport) Spans() []*tracer.Span {
	s := d.spans
	d.spans = nil
	return s
}
