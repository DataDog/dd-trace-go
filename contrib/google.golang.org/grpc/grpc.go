//go:generate protoc -I . fixtures_test.proto --go_out=plugins=grpc:.

// Package grpc provides functions to trace the google.golang.org/grpc package v1.2.
package grpc // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"

import (
	"io"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	context "golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func startSpanFromContext(ctx context.Context, method, operation, service string) (ddtrace.Span, context.Context) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(service),
		tracer.ResourceName(method),
		tracer.Tag(tagMethod, method),
		tracer.SpanType(ext.AppTypeRPC),
	}
	md, _ := metadata.FromIncomingContext(ctx) // nil is ok
	if sctx, err := tracer.Extract(grpcutil.MDCarrier(md)); err == nil {
		opts = append(opts, tracer.ChildOf(sctx))
	}
	return tracer.StartSpanFromContext(ctx, operation, opts...)
}

// finishWithError applies finish option and a tag with gRPC status code, disregarding OK, EOF and Canceled errors.
func finishWithError(span ddtrace.Span, err error) {
	errcode := status.Code(err)
	if err == io.EOF || errcode == codes.Canceled || errcode == codes.OK || err == context.Canceled {
		err = nil
	}
	span.SetTag(tagCode, errcode.String())
	span.Finish(tracer.WithError(err))
}
