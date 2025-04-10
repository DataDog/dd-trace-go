// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"

	"google.golang.org/grpc"
)

// StreamServerInterceptor will trace streaming requests to the given gRPC server.
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	return v2.StreamServerInterceptor(opts...)
}

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	return v2.UnaryServerInterceptor(opts...)
}
