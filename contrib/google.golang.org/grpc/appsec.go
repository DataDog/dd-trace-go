// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"encoding/json"
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// UnaryHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecUnaryHandlerMiddleware(span ddtrace.Span, handler grpc.UnaryHandler) grpc.UnaryHandler {
	httpsec.SetAppSecTags(span)
	return func(ctx context.Context, req interface{}) (interface{}, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		op := grpcsec.StartHandlerOperation(grpcsec.HandlerOperationArgs{Metadata: md}, nil)
		defer func() {
			events := op.Finish(grpcsec.HandlerOperationRes{})
			instrumentation.SetTags(span, op.Metrics())
			if len(events) == 0 {
				return
			}
			setAppSecTags(ctx, span, events)
		}()
		defer grpcsec.StartReceiveOperation(grpcsec.ReceiveOperationArgs{}, op).Finish(grpcsec.ReceiveOperationRes{Message: req})
		return handler(ctx, req)
	}
}

// StreamHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecStreamHandlerMiddleware(span ddtrace.Span, handler grpc.StreamHandler) grpc.StreamHandler {
	httpsec.SetAppSecTags(span)
	return func(srv interface{}, stream grpc.ServerStream) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		op := grpcsec.StartHandlerOperation(grpcsec.HandlerOperationArgs{Metadata: md}, nil)
		defer func() {
			events := op.Finish(grpcsec.HandlerOperationRes{})
			instrumentation.SetTags(span, op.Metrics())
			if len(events) == 0 {
				return
			}
			setAppSecTags(stream.Context(), span, events)
		}()
		return handler(srv, appsecServerStream{ServerStream: stream, handlerOperation: op})
	}
}

type appsecServerStream struct {
	grpc.ServerStream
	handlerOperation *grpcsec.HandlerOperation
}

// RecvMsg implements grpc.ServerStream interface method to monitor its
// execution with AppSec.
func (ss appsecServerStream) RecvMsg(m interface{}) error {
	op := grpcsec.StartReceiveOperation(grpcsec.ReceiveOperationArgs{}, ss.handlerOperation)
	defer func() {
		op.Finish(grpcsec.ReceiveOperationRes{Message: m})
	}()
	return ss.ServerStream.RecvMsg(m)
}

// Set the AppSec tags when security events were found.
func setAppSecTags(ctx context.Context, span ddtrace.Span, events []json.RawMessage) {
	md, _ := metadata.FromIncomingContext(ctx)
	var addr net.Addr
	if p, ok := peer.FromContext(ctx); ok {
		addr = p.Addr
	}
	grpcsec.SetSecurityEventTags(span, events, addr, md)
}
