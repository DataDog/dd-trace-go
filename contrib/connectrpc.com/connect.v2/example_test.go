// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect_test

import (
	"context"
	"log"
	"net"
	"net/http"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect.v2/v2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var pingSpec = connectrpc.Spec{StreamType: connectrpc.StreamTypeUnary, Procedure: "/acme.ping.v1.PingService/Ping"}

// ping is a unary method handler, like those that generated RegisterXHandler functions register.
func ping(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	var request emptypb.Empty
	if err := stream.Receive(&request); err != nil {
		return err
	}
	return stream.Send(&emptypb.Empty{})
}

func Example() {
	tracer.Start()
	defer tracer.Stop()

	// Every method registered on the server is traced, including those that generated
	// RegisterXServiceHandler functions register.
	server := connectrpc.NewServer(connecttrace.NewServerInterceptor())
	server.Register(connectrpc.Method{Spec: pingSpec, Handler: ping})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	listener, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Print(err)
		return
	}
	defer listener.Close()
	go http.Serve(listener, mux)

	// Every call made with the client is traced, including those made by the generated
	// NewXServiceClient clients that wrap it.
	client := connectrpc.NewClient(
		connecthttp.NewTransport(http.DefaultClient, "http://"+listener.Addr().String()),
		connecttrace.NewClientInterceptor(),
	)
	var response emptypb.Empty
	if err := client.CallUnary(context.Background(), pingSpec, &emptypb.Empty{}, &response); err != nil {
		log.Print(err)
	}
}

func ExampleNewClientInterceptor() {
	client := connectrpc.NewClient(
		connecthttp.NewTransport(http.DefaultClient, "https://example.com"),
		connecttrace.NewClientInterceptor(
			connecttrace.WithService("ping-client"),
			connecttrace.WithMetadataTags(),
		),
	)
	_ = client
}

func ExampleNewServerInterceptor() {
	server := connectrpc.NewServer(connecttrace.NewServerInterceptor(
		connecttrace.WithService("ping-server"),
		connecttrace.WithUntracedMethods("/grpc.health.v1.Health/Check"),
	))
	server.Register(connectrpc.Method{Spec: pingSpec, Handler: ping})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	_ = mux
}
