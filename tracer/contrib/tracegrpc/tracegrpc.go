package tracegrpc

import (
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Interceptor returns a UnaryServerInterceptor which will trace requests.
func Interceptor(t *tracer.Tracer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {

		if !t.Enabled() {
			return handler(ctx, req)
		}

		span := grpcSpan(t, ctx, info.FullMethod)
		resp, err := handler(tracer.ContextWithSpan(ctx, span), req)
		span.FinishWithErr(err)
		return resp, err
	}
}

// grpcSpan returns a new grpc span for the given request.
func grpcSpan(t *tracer.Tracer, ctx context.Context, method string) *tracer.Span {

	service, resource := parseMethod(method)

	span := t.NewRootSpan("grpc.server", service, resource)
	span.SetMeta("gprc.method", method)
	span.Type = "go"

	traceID, parentID := getIDs(ctx)
	if traceID != 0 && parentID != 0 {
		span.TraceID = traceID
		span.ParentID = parentID
	}

	return span
}

func parseMethod(method string) (service, resource string) {

	start := 0
	if len(method) > 0 && method[0] == '/' {
		start = 1
	}

	if idx := strings.LastIndexByte(method, '/'); idx > 0 {
		service = method[start:idx]
		method = method[idx+1:]
		return service, method
	}

	return "", ""
}

// getIDs will return ids embededd in the context.
func getIDs(ctx context.Context) (traceID, parentID uint64) {
	if md, ok := metadata.FromContext(ctx); ok {
		if id := getID(md, "trace_id"); id > 0 {
			traceID = id
		}
		if id := getID(md, "parent_id"); id > 0 {
			parentID = id
		}
	}
	return traceID, parentID
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
