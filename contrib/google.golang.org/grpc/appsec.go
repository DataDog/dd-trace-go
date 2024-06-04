// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/grpctrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/appsec-internal-go/netip"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecUnaryHandlerMiddleware(method string, span ddtrace.Span, handler grpc.UnaryHandler) grpc.UnaryHandler {
	trace.SetAppSecEnabledTags(span)
	return func(ctx context.Context, req any) (res any, rpcErr error) {
		var blockedErr error
		md, _ := metadata.FromIncomingContext(ctx)
		clientIP := setClientIP(ctx, span, md)
		args := types.HandlerOperationArgs{
			Method:   method,
			Metadata: md,
			ClientIP: clientIP,
		}
		ctx, op := grpcsec.StartHandlerOperation(ctx, args, nil, func(op *types.HandlerOperation) {
			dyngo.OnData(op, func(a *sharedsec.GRPCAction) {
				code, err := a.GRPCWrapper()
				blockedErr = status.Error(codes.Code(code), err.Error())
			})
		})
		defer func() {
			events := op.Finish(types.HandlerOperationRes{})
			if len(events) > 0 {
				grpctrace.SetSecurityEventsTags(span, events)
			}
			if blockedErr != nil {
				op.SetTag(trace.BlockedRequestTag, true)
				rpcErr = blockedErr
			}
			grpctrace.SetRequestMetadataTags(span, md)
			trace.SetTags(span, op.Tags())
		}()

		// Check if a blocking condition was detected so far with the start operation event (ip blocking, metadata blocking, etc.)
		if blockedErr != nil {
			return nil, blockedErr
		}

		// As of our gRPC abstract operation definition, we must fake a receive operation for unary RPCs (the same model fits both unary and streaming RPCs)
		grpcsec.StartReceiveOperation(types.ReceiveOperationArgs{}, op).Finish(types.ReceiveOperationRes{Message: req})
		// Check if a blocking condition was detected so far with the receive operation events
		if blockedErr != nil {
			return nil, blockedErr
		}

		// Call the original handler - let the deferred function above handle the blocking condition and return error
		return handler(ctx, req)
	}
}

// StreamHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecStreamHandlerMiddleware(method string, span ddtrace.Span, handler grpc.StreamHandler) grpc.StreamHandler {
	trace.SetAppSecEnabledTags(span)
	return func(srv any, stream grpc.ServerStream) (rpcErr error) {
		// Create a ServerStream wrapper with appsec RPC handler operation and the Go context (to implement the ServerStream interface)
		appsecStream := &appsecServerStream{
			ServerStream: stream,
			// note: the blockedErr field is captured by the RPC handler's OnData closure below
		}

		ctx := stream.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		clientIP := setClientIP(ctx, span, md)
		grpctrace.SetRequestMetadataTags(span, md)

		// Create the handler operation and listen to blocking gRPC actions to detect a blocking condition
		args := types.HandlerOperationArgs{
			Method:   method,
			Metadata: md,
			ClientIP: clientIP,
		}
		ctx, op := grpcsec.StartHandlerOperation(ctx, args, nil, func(op *types.HandlerOperation) {
			dyngo.OnData(op, func(a *sharedsec.GRPCAction) {
				code, e := a.GRPCWrapper()
				appsecStream.blockedErr = status.Error(codes.Code(code), e.Error())
			})
		})

		// Finish constructing the appsec stream wrapper and replace the original one
		appsecStream.handlerOperation = op
		appsecStream.ctx = ctx

		defer func() {
			events := op.Finish(types.HandlerOperationRes{})

			if len(events) > 0 {
				grpctrace.SetSecurityEventsTags(span, events)
			}

			if appsecStream.blockedErr != nil {
				op.SetTag(trace.BlockedRequestTag, true)
				// Change the RPC return error with appsec's
				rpcErr = appsecStream.blockedErr
			}

			trace.SetTags(span, op.Tags())
		}()

		// Check if a blocking condition was detected so far with the start operation event (ip blocking, metadata blocking, etc.)
		if appsecStream.blockedErr != nil {
			return appsecStream.blockedErr
		}

		// Call the original handler - let the deferred function above handle the blocking condition and return error
		return handler(srv, appsecStream)
	}
}

type appsecServerStream struct {
	grpc.ServerStream
	handlerOperation *types.HandlerOperation
	ctx              context.Context

	// blockedErr is used to store the error to return when a blocking sec event is detected.
	blockedErr error
}

// RecvMsg implements grpc.ServerStream interface method to monitor its
// execution with AppSec.
func (ss *appsecServerStream) RecvMsg(m interface{}) (err error) {
	op := grpcsec.StartReceiveOperation(types.ReceiveOperationArgs{}, ss.handlerOperation)
	defer func() {
		op.Finish(types.ReceiveOperationRes{Message: m})
		if ss.blockedErr != nil {
			// Change the function call return error with appsec's
			err = ss.blockedErr
		}
	}()
	return ss.ServerStream.RecvMsg(m)
}

func (ss *appsecServerStream) Context() context.Context {
	return ss.ctx
}

func setClientIP(ctx context.Context, span ddtrace.Span, md metadata.MD) netip.Addr {
	var remoteAddr string
	if p, ok := peer.FromContext(ctx); ok {
		remoteAddr = p.Addr.String()
	}
	ipTags, clientIP := httptrace.ClientIPTags(md, false, remoteAddr)
	log.Debug("appsec: http client ip detection returned `%s` given the http headers `%v`", clientIP, md)
	if len(ipTags) > 0 {
		trace.SetTags(span, ipTags)
	}
	return clientIP
}
