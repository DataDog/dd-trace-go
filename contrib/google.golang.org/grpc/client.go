// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"

	"google.golang.org/grpc"
)

// StreamClientInterceptor returns a grpc.StreamClientInterceptor which will trace client
// streams using the given set of options.
func StreamClientInterceptor(opts ...Option) grpc.StreamClientInterceptor {
	return v2.StreamClientInterceptor(opts...)
}

// UnaryClientInterceptor returns a grpc.UnaryClientInterceptor which will trace requests using
// the given set of options.
func UnaryClientInterceptor(opts ...Option) grpc.UnaryClientInterceptor {
	return v2.UnaryClientInterceptor(opts...)
}
