package grpc

import (
	context "golang.org/x/net/context"
	"google.golang.org/grpc/stats"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewServerStatsHandler returns a gRPC server stats.Handler to trace RPC calls.
func NewServerStatsHandler(opts ...StatsHandlerOption) stats.Handler {
	cfg := newStatsHandlerConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	return &serverStatsHandler{
		cfg: cfg,
	}
}

type serverStatsHandler struct {
	cfg *statsHandlerConfig
}

// TagRPC starts a new span for the initiated RPC request.
func (h *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	_, ctx = startSpanFromContext(ctx, rti.FullMethodName, "grpc.server", h.cfg.serverServiceName())
	return ctx
}

// HandleRPC processes the RPC ending event by finishing the span from the context.
func (h *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if v, ok := rs.(*stats.End); ok {
		finishWithError(span, v.Error, h.cfg.noDebugStack)
	}
}

// TagConn implements stats.Handler.
func (h *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn implements stats.Handler.
func (h *serverStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {}
