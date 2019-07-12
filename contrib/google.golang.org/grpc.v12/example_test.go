// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package grpc_test

import (
	"log"
	"net"

	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc.v12"

	"google.golang.org/grpc"
)

func Example_client() {
	// Create the client interceptor using the grpc trace package.
	i := grpctrace.UnaryClientInterceptor(grpctrace.WithServiceName("my-grpc-client"))

	// Dial in using the created interceptor...
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure(), grpc.WithUnaryInterceptor(i))
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
	i := grpctrace.UnaryServerInterceptor(grpctrace.WithServiceName("my-grpc-client"))

	// Initialize the grpc server as normal, using the tracing interceptor.
	s := grpc.NewServer(grpc.UnaryInterceptor(i))

	// ... register your services

	// Start serving incoming connections.
	if err := s.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
