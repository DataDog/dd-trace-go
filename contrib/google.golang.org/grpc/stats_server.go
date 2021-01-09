// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	context "golang.org/x/net/context"
	"google.golang.org/grpc/stats"
)

// NewServerStatsHandler returns a gRPC server stats.Handler to trace RPC calls.
func NewServerStatsHandler(opts ...Option) stats.Handler {
	cfg := new(config)
	defaults(cfg)
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
	_, ctx = startSpanFromContext(
		ctx,
		rti.FullMethodName,
		"grpc.server",
		h.cfg.serverServiceName(),
		tracer.AnalyticsRate(h.cfg.analyticsRate),
		tracer.Measured(),
	)
	return ctx
}

// HandleRPC processes the RPC ending event by finishing the span from the context.
func (h *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if v, ok := rs.(*stats.End); ok {
		finishWithError(span, v.Error, h.cfg)
	}
}

// TagConn implements stats.Handler.
func (h *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn implements stats.Handler.
func (h *serverStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {}
