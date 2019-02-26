package grpc

import (
	"net"

	context "golang.org/x/net/context"
	"google.golang.org/grpc/stats"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewClientStatsHandler returns gRPC client stats.Handler to record stats and traces.
func NewClientStatsHandler(opts ...StatsHandlerOption) stats.Handler {
	cfg := newStatsHandlerConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	return &clientStatsHandler{
		cfg: cfg,
	}
}

type clientStatsHandler struct {
	cfg *statsHandlerConfig
}

// TagRPC attaches some information to the given context.
func (h *clientStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	_, ctx = startSpanFromContext(ctx, rti.FullMethodName, "grpc.client", h.cfg.clientServiceName())
	ctx = injectSpanIntoContext(ctx)
	return ctx
}

// HandleRPC processes the RPC stats.
func (h *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
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
			finishWithError(span, rs.Error, h.cfg.noDebugStack)
		}
	}
}

// TagConn can attach some information to the given context.
// This method exists to satisfy gRPC stats.Handler.
func (h *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn processes the Conn stats.
// This method exists to satisfy gRPC stats.Handler.
func (h *clientStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {
}
