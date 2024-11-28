// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package go_control_plane_test

import (
	"google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/envoyproxy/go-control-plane"
	"log"
	"net"
)

func Example_server() {
	// Create a listener for the server.
	ln, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	// Create the server interceptor using the envoy go control plane package.
	si := go_control_plane.StreamServerInterceptor()

	// Initialize the grpc server as normal, using the envoy server interceptor.
	s := grpc.NewServer(grpc.StreamInterceptor(si))

	// ... register your services

	// Start serving incoming connections.
	if err := s.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
