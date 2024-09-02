package envoy

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	auth_pb "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	status "google.golang.org/genproto/googleapis/rpc/status"

	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	httpsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
)

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...grpctrace.Option) grpc.UnaryServerInterceptor {
	interceptor := grpctrace.UnaryServerInterceptor(opts...)

	// Leverage the External Auth specification at https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/auth/v3/external_auth.proto#envoy-v3-api-msg-service-auth-v3-checkrequest
	// to unwrap the HTTP request and be able to perform finer-grained APM tracing and AppSec monitoring of it.
	return func(ctx context.Context, rpcReq interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (rpcRes interface{}, rpcErr error) {
		// Get the HTTP request
		creq, ok := rpcReq.(*auth_pb.CheckRequest)
		if !ok {
			return interceptor(ctx, rpcReq, info, handler)
		}
		httpReq := creq.GetAttributes().GetRequest().GetHttp()
		if httpReq == nil {
			return interceptor(ctx, rpcReq, info, handler)
		}

		// TODO: split it to get the service entry span
		rpcRes, rpcErr = interceptor(ctx, rpcReq, info, handler)

		cres, ok := rpcRes.(*auth_pb.CheckResponse)
		if !ok {
			return rpcRes, rpcErr
		}

		// Create the HTTP operation AppSec listens to
		args := httpsectypes.HandlerOperationArgs{
			Method:  httpReq.Method,
			Headers: makeHeaders(httpReq),
		}

		// Try to parse HTTP Request URL
		u, err := url.Parse(httpReq.Path)
		if err == nil {
			args.RequestURI = u.RequestURI()
			args.Query = u.Query()
		}

		// TODO: client IP

		// TODO: parent gRPC operation to get AppSec gRPC monitoring too

		// Start the HTTP operation
		ctx, op := httpsec.StartOperation(ctx, args, func(op dyngo.Operation) {
			dyngo.OnData(op, func(a *sharedsec.HTTPAction) {
				// HTTP Blocking Action Handler
				if a.Blocking() {
					cres.Status = status.Status{Code: codes.PermissionDenied}
					// TODO: setup cres.DeniedResponse with AppSec's blocking response
				}
			})
		})

		// Finish the HTTP operation when returning
		defer func() {
			secEvents := op.Finish(httpsectypes.HandlerOperationRes{ /* nothing we can monitor here */ })
			if len(secEvents) > 0 {
				tag, err := trace.MakeEventsMetaTagValue(secEvents)
				if err != nil {
					cres.DynamicMetadata.Fields[]
				}
			}
		}()

		// TODO: appsec.MonitorRawHTTPBody(httpReq.Body or httpReq.RawBody)

		return rpcRes, rpcErr
	})
}

// makeHeaders recreates the map of headers in the expected format and
// normalized as expected by both AppSec and APM with lower-cased header names.
func makeHeaders(req *auth_pb.AttributeContext_HttpRequest) http.Header {
	h := req.GetHeaders()
	result := make(map[string][]string, len(h))

	// Convert the headers to a http.Header map
	for k, v := range h {
		k := strings.ToLower(k) // Normalize the header name by lowercasing it
		if k == "cookie" {
			continue // ignore cookies
			// TODO: parse and return cookies in a separate map to pass them to appsec
		}
		result[k] = strings.Split(v, ",")
	}

	// Add extra request values under their corresponding HTTP headers
	result["host"] = []string{req.GetHost()}
	result["x-request-id"] = []string{req.Id}
	return result
}
