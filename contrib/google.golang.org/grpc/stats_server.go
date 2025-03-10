// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"google.golang.org/grpc/stats"
)

// NewServerStatsHandler returns a gRPC server stats.Handler to trace RPC calls.
func NewServerStatsHandler(opts ...Option) stats.Handler {
	cfg := new(config)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &serverStatsHandler{
		cfg: cfg,
	}
}

type serverStatsHandler struct {
	cfg *config
}

// TagRPC starts a new span for the initiated RPC request.
func (h *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	spanOpts := append([]tracer.StartSpanOption{
		tracer.Measured(),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer)},
		h.cfg.spanOpts...,
	)
	_, ctx = startSpanFromContext(
		ctx,
		rti.FullMethodName,
		h.cfg.spanName,
		h.cfg.serviceName,
		spanOpts...,
	)
	ctx = context.WithValue(ctx, fullMethodNameKey{}, rti.FullMethodName)
	return ctx
}

// HandleRPC processes the RPC ending event by finishing the span from the context.
func (h *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}

	fullMethod, _ := ctx.Value(fullMethodNameKey{}).(string)
	if v, ok := rs.(*stats.End); ok {
		finishWithError(span, v.Error, fullMethod, h.cfg)
	}
}

// TagConn implements stats.Handler.
func (h *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn implements stats.Handler.
func (h *serverStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {}
