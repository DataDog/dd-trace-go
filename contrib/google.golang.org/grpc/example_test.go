package grpc_test

import (
	"log"
	"net"

	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"

	"google.golang.org/grpc"
)

func Example_client() {
	// Create the client interceptor using the grpc trace package.
	si := grpctrace.StreamClientInterceptor(grpctrace.WithServiceName("my-grpc-client"))
	ui := grpctrace.UnaryClientInterceptor(grpctrace.WithServiceName("my-grpc-client"))

	// Dial in using the created interceptor...
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure(),
		grpc.WithStreamInterceptor(si), grpc.WithUnaryInterceptor(ui))
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

	// Create the server interceptor using the grpc trace package.
	si := grpctrace.StreamServerInterceptor(grpctrace.WithServiceName("my-grpc-client"))
	ui := grpctrace.UnaryServerInterceptor(grpctrace.WithServiceName("my-grpc-client"))

	// Initialize the grpc server as normal, using the tracing interceptor.
	s := grpc.NewServer(grpc.StreamInterceptor(si), grpc.UnaryInterceptor(ui))

	// ... register your services

	// Start serving incoming connections.
	if err := s.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
