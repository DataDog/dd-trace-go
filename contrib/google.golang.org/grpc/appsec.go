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
	return func(ctx context.Context, req interface{}) (interface{}, error) {
		var err error
		var blocked bool
		md, _ := metadata.FromIncomingContext(ctx)
		clientIP := setClientIP(ctx, span, md)
		args := types.HandlerOperationArgs{
			Method:   method,
			Metadata: md,
			ClientIP: clientIP,
		}
		ctx, op := grpcsec.StartHandlerOperation(ctx, args, nil, func(op *types.HandlerOperation) {
			dyngo.OnData(op, func(a *sharedsec.GRPCAction) {
				code, e := a.GRPCWrapper()
				blocked = a.Blocking()
				err = status.Error(codes.Code(code), e.Error())
			})
		})
		defer func() {
			events := op.Finish(types.HandlerOperationRes{})
			if blocked {
				op.SetTag(trace.BlockedRequestTag, true)
			}
			grpctrace.SetRequestMetadataTags(span, md)
			trace.SetTags(span, op.Tags())
			if len(events) > 0 {
				grpctrace.SetSecurityEventsTags(span, events)
			}
		}()

		if err != nil {
			return nil, err
		}

		defer grpcsec.StartReceiveOperation(types.ReceiveOperationArgs{Message: req}, op).Finish(types.ReceiveOperationRes{})
		if err != nil {
			return nil, err
		}

		rv, err := handler(ctx, req)
		if e, ok := err.(*types.MonitoringError); ok {
			err = status.Error(codes.Code(e.GRPCStatus()), e.Error())
		}
		return rv, err
	}
}

// StreamHandler wrapper to use when AppSec is enabled to monitor its execution.
func appsecStreamHandlerMiddleware(method string, span ddtrace.Span, handler grpc.StreamHandler) grpc.StreamHandler {
	trace.SetAppSecEnabledTags(span)
	return func(srv interface{}, stream grpc.ServerStream) error {
		 appsecStream := &appsecServerStream{
			ServerStream:     stream,
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
				if a.Blocking() {
					code, e := a.GRPCWrapper()
					appsecStream.blockedErr = status.Error(codes.Code(code), e.Error())

				}
			})
		})

		appsecStream.handlerOperation = op
		appsecStream.ctx = ctx
		stream = appsecStream

		defer func() {
			events := op.Finish(types.HandlerOperationRes{})
			if appsecStream.blockedErr != nil {
				op.SetTag(trace.BlockedRequestTag, true)
			}
			trace.SetTags(span, op.Tags())
			if len(events) > 0 {
				grpctrace.SetSecurityEventsTags(span, events)
			}
		}()

		// Check if a blocking condition was detected so far with the start operation event (ip blocking, metadata blocking, etc.)
		if appsecStream.blockedErr != nil {
			return appsecStream.blockedErr
		}

		// Call the original handler
		err := handler(srv, stream)
		//if e, ok := err.(*types.MonitoringError); ok {
		//	err = status.Error(codes.Code(e.GRPCStatus()), e.Error())
		//}
		return err
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
func (ss appsecServerStream) RecvMsg(m interface{}) (err error) {
	op := grpcsec.StartReceiveOperation(types.ReceiveOperationArgs{Message: m}, ss.handlerOperation)
	defer op.Finish(types.ReceiveOperationRes{})

	if ss.blockedErr != nil {
		return ss.blockedErr
	}

	return ss.ServerStream.RecvMsg(m)
}

func (ss appsecServerStream) Context() context.Context {
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
