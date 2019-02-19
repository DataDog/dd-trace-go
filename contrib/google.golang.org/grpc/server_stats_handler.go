package grpc

import (
	context "golang.org/x/net/context"
	"google.golang.org/grpc/stats"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewServerStatsHandler returns gRPC server stats.Handler to record stats and traces.
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

// TagRPC attaches some information to the given context.
func (h *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	_, ctx = startSpanFromContext(ctx, rti.FullMethodName, "grpc.server", h.cfg.serverServiceName())
	return ctx
}

// HandleRPC processes the RPC stats.
func (h *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		switch rs := rs.(type) {
		case *stats.End:
			finishWithError(span, rs.Error, h.cfg.noDebugStack)
		}
	}
}

// TagConn can attach some information to the given context.
// This method exists to satisfy gRPC stats.Handler.
func (h *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn processes the Conn stats.
// This method exists to satisfy gRPC stats.Handler.
func (h *serverStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {
}
