// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package pubsubtrace

import (
	"context"
	"strings"

	"google.golang.org/grpc"
)

// adminResolver returns the resource path targeted by an admin request, or an
// empty string when req is not a traced admin operation (data-plane, IAM, ...).
type adminResolver func(req any) (resourcePath string)

// unaryAdminInterceptor builds a grpc.UnaryClientInterceptor that emits a
// gcp.pubsub.request span for each admin RPC recognised by resolve.
func (tr *Tracer) unaryAdminInterceptor(resolve adminResolver, opts ...Option) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
		resourcePath := resolve(req)
		if resourcePath == "" {
			return invoker(ctx, method, req, reply, cc, callOpts...)
		}
		ctx, finish := tr.TraceAdmin(ctx, adminMethodName(method), resourcePath, opts...)
		err := invoker(ctx, method, req, reply, cc, callOpts...)
		finish(err)
		return err
	}
}

// adminMethodName returns the RPC method name from a gRPC full-method string, e.g.
// "/google.pubsub.v1.Publisher/CreateTopic" -> "CreateTopic".
func adminMethodName(fullMethod string) string {
	if i := strings.LastIndex(fullMethod, "/"); i >= 0 {
		return fullMethod[i+1:]
	}
	return fullMethod
}
