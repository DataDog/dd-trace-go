// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"net"

	context "golang.org/x/net/context"
	"google.golang.org/grpc/stats"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewClientStatsHandler returns a gRPC client stats.Handler to trace RPC calls.
func NewClientStatsHandler(opts ...Option) stats.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &clientStatsHandler{
		cfg: cfg,
	}
}

type clientStatsHandler struct{ cfg *config }

// TagRPC starts a new span for the initiated RPC request.
func (h *clientStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	_, ctx = startSpanFromContext(
		ctx,
		rti.FullMethodName,
		"grpc.client",
		h.cfg.clientServiceName(),
		tracer.AnalyticsRate(h.cfg.analyticsRate),
	)
	ctx = injectSpanIntoContext(ctx)
	return ctx
}

// HandleRPC processes the RPC ending event by finishing the span from the context.
func (h *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	switch rs := rs.(type) {
	case *stats.OutHeader:
		host, port, err := net.SplitHostPort(rs.RemoteAddr.String())
		if err == nil {
			if host != "" {
				span.SetTag(ext.TargetHost, host)
			}
			span.SetTag(ext.TargetPort, port)
		}
	case *stats.End:
		finishWithError(span, rs.Error, h.cfg)
	}
}

// TagConn implements stats.Handler.
func (h *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn implements stats.Handler.
func (h *clientStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {}
