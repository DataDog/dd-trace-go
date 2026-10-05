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

	connectrpc "connectrpc.com/connect"
	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const pingProcedure = "/acme.ping.v1.PingService/Ping"

func ping(context.Context, *connectrpc.Request[emptypb.Empty]) (*connectrpc.Response[emptypb.Empty], error) {
	return connectrpc.NewResponse(&emptypb.Empty{}), nil
}

func Example() {
	tracer.Start()
	defer tracer.Stop()

	// The same interceptor option traces handlers and clients.
	traced := connectrpc.WithInterceptors(connecttrace.NewInterceptor())

	mux := http.NewServeMux()
	mux.Handle(pingProcedure, connectrpc.NewUnaryHandler(pingProcedure, ping, traced))
	listener, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Print(err)
		return
	}
	defer listener.Close()
	go http.Serve(listener, mux)

	client := connectrpc.NewClient[emptypb.Empty, emptypb.Empty](http.DefaultClient, "http://"+listener.Addr().String()+pingProcedure, traced)
	if _, err := client.CallUnary(context.Background(), connectrpc.NewRequest(&emptypb.Empty{})); err != nil {
		log.Print(err)
	}
}

func ExampleNewClientInterceptor() {
	interceptor := connecttrace.NewClientInterceptor()
	client := connectrpc.NewClient[emptypb.Empty, emptypb.Empty](
		http.DefaultClient,
		"https://example.com"+pingProcedure,
		connectrpc.WithInterceptors(interceptor),
	)
	_ = client
}

func ExampleNewServerInterceptor() {
	interceptor := connecttrace.NewServerInterceptor()
	handler := connectrpc.NewUnaryHandler(pingProcedure, ping, connectrpc.WithInterceptors(interceptor))
	_ = handler
}
