package tracegrpc

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// pass trace ids with these headers
const (
	traceIDKey          = "x-datadog-trace-id"
	parentIDKey         = "x-datadog-parent-id"
	samplingPriorityKey = "x-datadog-sampling-priority"
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

	traceID, parentID, samplingPriority := getCtxMeta(ctx)
	if traceID != 0 && parentID != 0 {
		span.TraceID = traceID
		t.Sample(span) // depends on trace ID so needs to be updated to maximize the chances we get complete traces
		span.ParentID = parentID
		span.SetSamplingPriority(samplingPriority)
	}

	return span
}

// setCtxMeta will set the trace ids and the sampling priority on the context.
func setCtxMeta(span *tracer.Span, ctx context.Context) context.Context {
	if span == nil || span.TraceID == 0 {
		return ctx
	}

	md := metadata.New(map[string]string{
		traceIDKey:          strconv.FormatUint(span.TraceID, 10),
		parentIDKey:         strconv.FormatUint(span.ParentID, 10),
		samplingPriorityKey: strconv.Itoa(span.GetSamplingPriority()),
	})
	if existing, ok := metadata.FromContext(ctx); ok {
		md = metadata.Join(existing, md)
	}
	return metadata.NewContext(ctx, md)
}

// getCtxMeta will return ids and sampling priority embedded in a context.
func getCtxMeta(ctx context.Context) (traceID, parentID uint64, samplingPriority int) {
	if md, ok := metadata.FromContext(ctx); ok {
		if id := getID(md, traceIDKey); id > 0 {
			traceID = id
		}
		if id := getID(md, parentIDKey); id > 0 {
			parentID = id
		}
		if v := getInt(md, samplingPriorityKey); v > 0 {
			samplingPriority = v
		}
	}
	return traceID, parentID, samplingPriority
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
func getInt(md metadata.MD, name string) int {
	for _, str := range md[name] {
		v, err := strconv.Atoi(str)
		if err == nil {
			return int(v)
		}
	}
	return 0
}
