// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect_test

import (
	"context"
	"net/http"

	connecttrace "github.com/DataDog/dd-trace-go/contrib/connectrpc/connect-go/v2"

	"connectrpc.com/connect"
)

func Example_client() {
	// Create the Connect interceptor for client-side tracing.
	interceptor := connecttrace.NewInterceptor(connecttrace.WithService("my-connect-client"))

	// Use the interceptor with a Connect client.
	_ = connect.NewClient[any, any](
		http.DefaultClient,
		"http://localhost:8080/acme.foo.v1.FooService/Bar",
		connect.WithInterceptors(interceptor),
	)
}

func Example_server() {
	// Create the Connect interceptor for server-side tracing.
	interceptor := connecttrace.NewInterceptor(connecttrace.WithService("my-connect-server"))

	// Use the interceptor with a Connect handler.
	mux := http.NewServeMux()
	mux.Handle("/acme.foo.v1.FooService/Bar", connect.NewUnaryHandler(
		"/acme.foo.v1.FooService/Bar",
		func(_ context.Context, _ *connect.Request[any]) (*connect.Response[any], error) {
			return connect.NewResponse[any](nil), nil
		},
		connect.WithInterceptors(interceptor),
	))
	_ = mux
}
