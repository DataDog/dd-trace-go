// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"log"
	"net"

	"google.golang.org/grpc"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// interface fpr external processing server
type envoyExtProcServer struct {
	extprocv3.ExternalProcessorServer
}

func Example_server() {
	// Create a listener for the server.
	ln, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	// Initialize the grpc server as normal, using the envoy server interceptor.
	s := grpc.NewServer()
	srv := &envoyExtProcServer{}

	// Register the appsec envoy external processor service
	appsecSrv := AppsecEnvoyExternalProcessorServer(srv, AppsecEnvoyConfig{
		IsGCPServiceExtension: false,
		BlockingUnavailable:   false,
	})

	// Enable the request counter that reports the number of analyzed requests every minute
	appsecSrv.StartRequestCounterReporting()

	extprocv3.RegisterExternalProcessorServer(s, appsecSrv)

	// ... register your services

	// Start serving incoming connections.
	if err := s.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
		appsecSrv.StopRequestCounterReporting()
	}
}
