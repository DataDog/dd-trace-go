// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"encoding/json"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecUnaryHandlerMiddleware(span ddtrace.Span, handler grpc.UnaryHandler) grpc.UnaryHandler {
	instrumentation.SetAppSecEnabledTags(span)
	return func(ctx context.Context, req interface{}) (interface{}, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		var remoteAddr string
		if p, ok := peer.FromContext(ctx); ok {
			remoteAddr = p.Addr.String()
		}
		ipTags, clientIP := httpsec.ClientIPTags(md, remoteAddr)
		instrumentation.SetStringTags(span, ipTags)

		ctx, op := grpcsec.StartHandlerOperation(ctx, grpcsec.HandlerOperationArgs{Metadata: md, ClientIP: clientIP}, nil)
		defer func() {
			events := op.Finish(grpcsec.HandlerOperationRes{})
			instrumentation.SetTags(span, op.Tags())
			if len(events) == 0 {
				return
			}
			setAppSecEventsTags(ctx, span, events)
		}()

		if op.BlockedCode != nil {
			op.AddTag(httpsec.BlockedRequestTag, true)
			return nil, status.Errorf(*op.BlockedCode, "Request blocked")
		}

		defer grpcsec.StartReceiveOperation(grpcsec.ReceiveOperationArgs{}, op).Finish(grpcsec.ReceiveOperationRes{Message: req})
		return handler(ctx, req)
	}
}

// StreamHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecStreamHandlerMiddleware(span ddtrace.Span, handler grpc.StreamHandler) grpc.StreamHandler {
	instrumentation.SetAppSecEnabledTags(span)
	return func(srv interface{}, stream grpc.ServerStream) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		var remoteAddr string
		if p, ok := peer.FromContext(stream.Context()); ok {
			remoteAddr = p.Addr.String()
		}
		ipTags, clientIP := httpsec.ClientIPTags(md, remoteAddr)
		instrumentation.SetStringTags(span, ipTags)

		ctx, op := grpcsec.StartHandlerOperation(stream.Context(), grpcsec.HandlerOperationArgs{Metadata: md, ClientIP: clientIP}, nil)
		stream = appsecServerStream{
			ServerStream:     stream,
			handlerOperation: op,
			ctx:              ctx,
		}
		defer func() {
			events := op.Finish(grpcsec.HandlerOperationRes{})
			instrumentation.SetTags(span, op.Tags())
			if len(events) == 0 {
				return
			}
			setAppSecEventsTags(stream.Context(), span, events)
		}()

		if op.BlockedCode != nil {
			op.AddTag(httpsec.BlockedRequestTag, true)
			return status.Error(*op.BlockedCode, "Request blocked")
		}

		return handler(srv, stream)
	}
}

type appsecServerStream struct {
	grpc.ServerStream
	handlerOperation *grpcsec.HandlerOperation
	ctx              context.Context
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

func (ss appsecServerStream) Context() context.Context {
	return ss.ctx
}

// Set the AppSec tags when security events were found.
func setAppSecEventsTags(ctx context.Context, span ddtrace.Span, events []json.RawMessage) {
	md, _ := metadata.FromIncomingContext(ctx)
	grpcsec.SetSecurityEventTags(span, events, md)
}
