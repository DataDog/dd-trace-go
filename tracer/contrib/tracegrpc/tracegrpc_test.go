package tracegrpc

import (
	"net"
	"testing"

	"google.golang.org/grpc"

	context "golang.org/x/net/context"

	"github.com/stretchr/testify/assert"
)

func TestServer(t *testing.T) {
	assert := assert.New(t)
	srv := grpc.NewServer()
	RegisterFixtureServer(srv, newServer())
	assert.NotNil(srv)

	li, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	defer li.Close()

	// start our test server.
	go srv.Serve(li)
	defer srv.Stop()

	conn, err := grpc.Dial(li.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("failed to establish grpc connection: %v", err)
	}
	defer conn.Close()

	client := NewFixtureClient(conn)
	resp, err := client.Ping(context.Background(), &FixtureRequest{Name: "foo"})
	assert.Nil(err)
	assert.Equal(resp.Message, "Fixture foo")
}

// server a dummy implemenation of our grpc server.
type server struct{}

func newServer() *server {
	return &server{}
}
func (s *server) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	return &FixtureReply{Message: "Fixture " + in.Name}, nil
}

// ensure it's a server
var _ FixtureServer = &server{}
