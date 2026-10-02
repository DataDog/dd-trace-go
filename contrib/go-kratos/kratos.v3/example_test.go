// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos_test

import (
	"context"

	kratostrace "github.com/DataDog/dd-trace-go/contrib/go-kratos/kratos.v3/v2"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func ExampleServer() {
	tracer.Start()
	defer tracer.Stop()

	httpServer := kratoshttp.NewServer(
		kratoshttp.Address(":8000"),
		kratoshttp.Middleware(kratostrace.Server()),
	)
	grpcServer := kratosgrpc.NewServer(
		kratosgrpc.Address(":9000"),
		kratosgrpc.Middleware(kratostrace.Server()),
	)
	_, _ = httpServer, grpcServer
}

func ExampleClient() {
	tracer.Start()
	defer tracer.Stop()

	httpClient, err := kratoshttp.NewClient(
		context.Background(),
		kratoshttp.WithEndpoint("http://127.0.0.1:8000"),
		kratoshttp.WithMiddleware(kratostrace.Client()),
	)
	if err != nil {
		return
	}
	defer httpClient.Close()

	grpcClient, err := kratosgrpc.NewClient(
		context.Background(),
		kratosgrpc.WithEndpoint("127.0.0.1:9000"),
		kratosgrpc.WithMiddleware(kratostrace.Client()),
	)
	if err != nil {
		return
	}
	defer grpcClient.Close()
}
