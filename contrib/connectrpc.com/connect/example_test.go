// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect_test

import (
	"context"
	"net/http"

	connectrpc "connectrpc.com/connect"
	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/emptypb"
)

func ExampleNewClientInterceptor() {
	interceptor := connecttrace.NewClientInterceptor()
	client := connectrpc.NewClient[emptypb.Empty, emptypb.Empty](
		http.DefaultClient,
		"https://example.com/acme.ping.v1.PingService/Ping",
		connectrpc.WithInterceptors(interceptor),
	)
	_ = client
}

func ExampleNewServerInterceptor() {
	interceptor := connecttrace.NewServerInterceptor()
	handler := connectrpc.NewUnaryHandler(
		"/acme.ping.v1.PingService/Ping",
		func(context.Context, *connectrpc.Request[emptypb.Empty]) (*connectrpc.Response[emptypb.Empty], error) {
			return connectrpc.NewResponse(&emptypb.Empty{}), nil
		},
		connectrpc.WithInterceptors(interceptor),
	)
	_ = handler
}
