// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"
)

func applyAction(blockAtomic *atomic.Pointer[actions.BlockGRPC], err *error) bool {
	if blockAtomic == nil {
		return false
	}

	block := blockAtomic.Load()
	if block == nil {
		return false
	}

	code, e := block.GRPCWrapper()
	*err = status.Error(codes.Code(code), e.Error())
	return true
}

// UnaryHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecUnaryHandlerMiddleware(method string, span ddtrace.Span, handler grpc.UnaryHandler) grpc.UnaryHandler {
	return func(ctx context.Context, req any) (res any, rpcErr error) {
		md, _ := metadata.FromIncomingContext(ctx)
		var remoteAddr string
		if p, ok := peer.FromContext(ctx); ok {
			remoteAddr = p.Addr.String()
		}

		ctx, op, blockAtomic := grpcsec.StartHandlerOperation(ctx, grpcsec.HandlerOperationArgs{
			Method:     method,
			Metadata:   md,
			RemoteAddr: remoteAddr,
		})

		defer func() {
			var statusCode int
			if statusErr, ok := rpcErr.(interface{ GRPCStatus() *status.Status }); ok && !applyAction(blockAtomic, &rpcErr) {
				statusCode = int(statusErr.GRPCStatus().Code())
			}
			op.Finish(span, grpcsec.HandlerOperationRes{StatusCode: statusCode})
			applyAction(blockAtomic, &rpcErr)
		}()

		// Check if a blocking condition was detected so far with the start operation event (ip blocking, metadata blocking, etc.)
		if applyAction(blockAtomic, &rpcErr) {
			return
		}

		// As of our gRPC abstract operation definition, we must fake a receive operation for unary RPCs (the same model fits both unary and streaming RPCs)
		if _ = grpcsec.MonitorRequestMessage(ctx, req); applyAction(blockAtomic, &rpcErr) {
			return
		}

		defer func() {
			_ = grpcsec.MonitorResponseMessage(ctx, res)
			applyAction(blockAtomic, &rpcErr)
		}()

		// Call the original handler - let the deferred function above handle the blocking condition and return error
		return handler(ctx, req)
	}
}

// StreamHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecStreamHandlerMiddleware(method string, span ddtrace.Span, handler grpc.StreamHandler) grpc.StreamHandler {
	return func(srv any, stream grpc.ServerStream) (rpcErr error) {
		ctx := stream.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		var remoteAddr string
		if p, ok := peer.FromContext(ctx); ok {
			remoteAddr = p.Addr.String()
		}

		// Create the handler operation and listen to blocking gRPC actions to detect a blocking condition
		ctx, op, blockAtomic := grpcsec.StartHandlerOperation(ctx, grpcsec.HandlerOperationArgs{
			Method:     method,
			Metadata:   md,
			RemoteAddr: remoteAddr,
		})

		// Create a ServerStream wrapper with appsec RPC handler operation and the Go context (to implement the ServerStream interface)

		defer func() {
			var statusCode int
			if res, ok := rpcErr.(interface{ Status() codes.Code }); ok && !applyAction(blockAtomic, &rpcErr) {
				statusCode = int(res.Status())
			}

			op.Finish(span, grpcsec.HandlerOperationRes{StatusCode: statusCode})
			applyAction(blockAtomic, &rpcErr)
		}()

		// Check if a blocking condition was detected so far with the start operation event (ip blocking, metadata blocking, etc.)
		if applyAction(blockAtomic, &rpcErr) {
			return
		}

		// Call the original handler - let the deferred function above handle the blocking condition and return error
		return handler(srv, &appsecServerStream{
			ServerStream:     stream,
			handlerOperation: op,
			ctx:              ctx,
			action:           blockAtomic,
			rpcErr:           &rpcErr,
		})
	}
}

type appsecServerStream struct {
	grpc.ServerStream
	handlerOperation *grpcsec.HandlerOperation
	ctx              context.Context
	action           *atomic.Pointer[actions.BlockGRPC]
	rpcErr           *error
}

// RecvMsg implements grpc.ServerStream interface method to monitor its
// execution with AppSec.
func (ss *appsecServerStream) RecvMsg(msg any) (err error) {
	defer func() {
		if _ = grpcsec.MonitorRequestMessage(ss.ctx, msg); applyAction(ss.action, ss.rpcErr) {
			err = *ss.rpcErr
		}
	}()
	return ss.ServerStream.RecvMsg(msg)
}

func (ss *appsecServerStream) SendMsg(msg any) error {
	if _ = grpcsec.MonitorResponseMessage(ss.ctx, msg); applyAction(ss.action, ss.rpcErr) {
		return *ss.rpcErr
	}
	return ss.ServerStream.SendMsg(msg)
}

func (ss *appsecServerStream) Context() context.Context {
	return ss.ctx
}
