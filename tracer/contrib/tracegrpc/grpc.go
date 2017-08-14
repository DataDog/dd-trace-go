package tracegrpc

import (
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// pass trace ids with these headers
const (
	traceIDKey   = "x-datadog-trace-id"
	parentIDKey  = "x-datadog-parent-id"
	isSampledKey = "x-datadog-is-sampled"
)

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(service string, t *tracer.Tracer) grpc.UnaryServerInterceptor {
	t.SetServiceInfo(service, "grpc-server", ext.AppTypeRPC)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !t.Enabled() {
			return handler(ctx, req)
		}

		span := serverSpan(t, ctx, info.FullMethod, service)
		resp, err := handler(tracer.ContextWithSpan(ctx, span), req)
		span.FinishWithErr(err)
		return resp, err
	}
}

// UnaryClientInterceptor will add tracing to a gprc client.
func UnaryClientInterceptor(service string, t *tracer.Tracer) grpc.UnaryClientInterceptor {
	t.SetServiceInfo(service, "grpc-client", ext.AppTypeRPC)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {

		var child *tracer.Span
		span, ok := tracer.SpanFromContext(ctx)

		// only trace the request if this is already part of a trace.
		// does this make sense?
		if ok && span.Tracer() != nil {
			t := span.Tracer()
			child = t.NewChildSpan("grpc.client", span)
			child.SetMeta("grpc.method", method)
			ctx = setCtxMeta(child, ctx)
			ctx = tracer.ContextWithSpan(ctx, child)
			// FIXME[matt] add the host / port information here
			// https://github.com/grpc/grpc-go/issues/951
		}

		err := invoker(ctx, method, req, reply, cc, opts...)
		if child != nil {
			child.SetMeta("grpc.code", grpc.Code(err).String())
			child.FinishWithErr(err)

		}
		return err
	}
}

func serverSpan(t *tracer.Tracer, ctx context.Context, method, service string) *tracer.Span {
	span := t.NewRootSpan("grpc.server", service, method)
	span.SetMeta("gprc.method", method)
	span.Type = "go"

	traceID, parentID := getIDs(ctx)
	if traceID != 0 && parentID != 0 {
		span.TraceID = traceID
		t.Sample(span) // depends on trace ID so needs to be updated to maximize the chances we get complete traces
		span.ParentID = parentID
		if isSampled, ok := getIsSampled(ctx); ok {
			span.DistributedSampled = isSampled
		}
	}

	return span
}

// setCtxMeta will set the trace ids and the IsSampled attribute on the context.
func setCtxMeta(span *tracer.Span, ctx context.Context) context.Context {
	if span == nil || span.TraceID == 0 {
		return ctx
	}

	isSampled := "0"
	if span.DistributedSampled {
		isSampled = "1"
	}
	md := metadata.New(map[string]string{
		traceIDKey:   fmt.Sprint(span.TraceID),
		parentIDKey:  fmt.Sprint(span.ParentID),
		isSampledKey: isSampled,
	})
	if existing, ok := metadata.FromContext(ctx); ok {
		md = metadata.Join(existing, md)
	}
	return metadata.NewContext(ctx, md)
}

// getIDs will return ids embedded in a context.
func getIDs(ctx context.Context) (traceID, parentID uint64) {
	if md, ok := metadata.FromContext(ctx); ok {
		if id := getID(md, traceIDKey); id > 0 {
			traceID = id
		}
		if id := getID(md, parentIDKey); id > 0 {
			parentID = id
		}
	}
	return traceID, parentID
}

// getIsSampled will return the isSampled embedded in a context.
func getIsSampled(ctx context.Context) (isSampled, ok bool) {
	if md, ok := metadata.FromContext(ctx); ok {
		if b, found := getBool(md, isSampledKey); found {
			ok = true
			isSampled = b
		} else {
			isSampled = true
		}
	}
	return isSampled, ok
}

// getID parses an id from the metadata.
func getID(md metadata.MD, name string) uint64 {
	for _, str := range md[name] {
		id, err := strconv.Atoi(str)
		if err == nil {
			return uint64(id)
		}
	}
	return 0
}

// getBool gets a bool from the metadata (0 or 1 converted to bool).
func getBool(md metadata.MD, name string) (bool, bool) {
	for _, str := range md[name] {
		if str == "0" {
			return false, true
		}
		n, err := strconv.Atoi(str)
		if err == nil && n > 0 {
			return true, true
		}
	}
	return true, false
}
