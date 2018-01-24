package grpc_test

import (
	"log"
	"net"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc"
	"github.com/DataDog/dd-trace-go/tracer"

	"google.golang.org/grpc"
)

func Example_client() {
	// Create the client interceptor using the grpc trace package.
	i := grpctrace.UnaryClientInterceptor("my-grpc-client", tracer.DefaultTracer)

	// Create initialization options for dialing into a server. Make sure
	// to include the created interceptor.
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithUnaryInterceptor(i),
	}

	// Dial in...
	conn, err := grpc.Dial("localhost:50051", opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// And continue using the connection as normal.
}

func Example_server() {
	// Create a listener for the server.
	ln, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	// Create the unary server interceptor using the grpc trace package.
	i := grpctrace.UnaryServerInterceptor("my-grpc-client", tracer.DefaultTracer)

	// Initialize the grpc server as normal, using the tracing interceptor.
	s := grpc.NewServer(grpc.UnaryInterceptor(i))

	// ... register your services

	// Start serving incoming connections.
	if err := s.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
