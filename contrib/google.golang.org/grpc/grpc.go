// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package grpc provides functions to trace the google.golang.org/grpc package v1.2.
package grpc // import "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2/internal/grpcutil"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoimpl"
)

const componentName = "google.golang.org/grpc"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGRPC)
}

// cache a constant option: saves one allocation per call
var spanTypeRPC = tracer.SpanType(ext.AppTypeRPC)

func (cfg *config) startSpanOptions(opts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	if len(cfg.tags) == 0 && len(cfg.spanOpts) == 0 {
		return opts
	}

	ret := make([]tracer.StartSpanOption, 0, 1+len(cfg.tags)+len(opts))
	for _, opt := range opts {
		ret = append(ret, opt)
	}
	for _, opt := range cfg.spanOpts {
		ret = append(ret, opt)
	}
	for key, tag := range cfg.tags {
		ret = append(ret, tracer.Tag(key, tag))
	}
	return ret
}

func startSpanFromContext(
	ctx context.Context, method, operation string, serviceName string, opts ...tracer.StartSpanOption,
) (*tracer.Span, context.Context) {
	methodElements := strings.SplitN(strings.TrimPrefix(method, "/"), "/", 2)
	opts = append(opts,
		tracer.ServiceName(serviceName),
		tracer.ResourceName(method),
		tracer.Tag(tagMethodName, method),
		spanTypeRPC,
		tracer.Tag(ext.RPCSystem, ext.RPCSystemGRPC),
		tracer.Tag(ext.GRPCFullMethod, method),
		tracer.Tag(ext.RPCService, methodElements[0]),
	)
	md, _ := metadata.FromIncomingContext(ctx) // nil is ok
	if sctx, err := tracer.Extract(grpcutil.MDCarrier(md)); err == nil {
		// If there are span links as a result of context extraction, add them as a StartSpanOption
		if sctx != nil && sctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(sctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(sctx))
	}
	return tracer.StartSpanFromContext(ctx, operation, opts...)
}

// finishWithError applies finish option and a tag with gRPC status code, disregarding OK, EOF and Canceled errors.
func finishWithError(span *tracer.Span, err error, cfg *config) {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	errcode := status.Code(err)
	if errcode == codes.OK || cfg.nonErrorCodes[errcode] {
		err = nil
	}
	span.SetTag(tagCode, errcode.String())
	if e, ok := status.FromError(err); ok && cfg.withErrorDetailTags {
		for i, d := range e.Details() {
			if d, ok := d.(proto.Message); ok {
				s := protoimpl.X.MessageStringOf(d)
				span.SetTag(tagStatusDetailsPrefix+fmt.Sprintf("_%d", i), s)
			}
		}
	}

	// only allocate finishOptions if needed, and allocate the exact right size
	var finishOptions []tracer.FinishOption
	if err != nil {
		if cfg.noDebugStack {
			finishOptions = []tracer.FinishOption{tracer.WithError(err), tracer.NoDebugStack()}
		} else {
			finishOptions = []tracer.FinishOption{tracer.WithError(err)}
		}
	}
	span.Finish(finishOptions...)
}
