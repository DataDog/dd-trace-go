// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package envoy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/trace"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	v32 "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	httpsec2 "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type CurrentRequest struct {
	op          *httpsec.HandlerOperation
	blockAction *atomic.Pointer[actions.BlockHTTP]
	span        tracer.Span

	remoteAddr  string
	parsedUrl   *url.URL
	requestArgs httpsec.HandlerOperationArgs

	statusCode int
	blocked    bool
}

func getRemoteAddr(xfwd []string) string {
	length := len(xfwd)
	if length == 0 {
		return ""
	}

	// Get the first right value of x-forwarded-for header
	// The rightmost IP address is the one that will be used as the remote client IP
	// https://datadoghq.atlassian.net/wiki/spaces/TS/pages/2766733526/Sensitive+IP+information#Where-does-the-value-of-the-http.client_ip-tag-come-from%3F
	return xfwd[length-1]
}

func StreamServerInterceptor(opts ...grpctrace.Option) grpc.StreamServerInterceptor {
	interceptor := grpctrace.StreamServerInterceptor(opts...)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != extproc.ExternalProcessor_Process_FullMethodName {
			return interceptor(srv, ss, info, handler)
		}

		ctx := ss.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		currentRequest := &CurrentRequest{
			blocked:    false,
			remoteAddr: getRemoteAddr(md.Get("x-forwarded-for")),
		}

		// Close the span when the request is done processing
		defer func() {
			closeSpan(currentRequest)
		}()

		for {
			select {
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.Canceled) {
					return nil
				}

				return ctx.Err()

			default:
			}

			var req extproc.ProcessingRequest
			err := ss.RecvMsg(&req)
			if err != nil {
				// Note: Envoy is inconsistent with the "end_of_stream" value of its headers responses,
				// so we can't fully rely on it to determine when it will close (cancel) the stream.
				if err == io.EOF || err.(interface{ GRPCStatus() *status.Status }).GRPCStatus().Code() == codes.Canceled {
					return nil
				}

				log.Warn("external_processing: error receiving request/response: %v\n", err)
				return status.Errorf(codes.Unknown, "Error receiving request/response: %v", err)
			}

			resp, err := envoyExternalProcessingEventHandler(ctx, &req, currentRequest)
			if err != nil {
				log.Error("external_processing: error processing request/response: %v\n", err)
				return status.Errorf(codes.Unknown, "Error processing request/response: %v", err)
			}

			// End of stream reached, no more data to process
			if resp == nil {
				log.Debug("external_processing: end of stream reached")
				return nil
			}

			// Send Message could fail if envoy close the stream before the message could be sent (probably because of an Envoy timeout)
			if err := ss.SendMsg(resp); err != nil {
				log.Warn("external_processing: error sending response (probably because of an Envoy timeout): %v", err)
				return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %v", err)
			}

			if currentRequest.blocked {
				log.Debug("external_processing: request blocked, stream ended")
				return nil
			}
		}
	}
}

func envoyExternalProcessingEventHandler(ctx context.Context, req *extproc.ProcessingRequest, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, error) {
	switch v := req.Request.(type) {
	case *extproc.ProcessingRequest_RequestHeaders:
		return ProcessRequestHeaders(ctx, req.Request.(*extproc.ProcessingRequest_RequestHeaders), currentRequest)

	case *extproc.ProcessingRequest_RequestBody:
		// TODO: Handle request raw body in the WAF
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestBody{
				RequestBody: &extproc.BodyResponse{
					Response: &extproc.CommonResponse{
						Status: extproc.CommonResponse_CONTINUE,
					},
				},
			},
		}, nil

	case *extproc.ProcessingRequest_RequestTrailers:
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestTrailers{},
		}, nil

	case *extproc.ProcessingRequest_ResponseHeaders:
		return ProcessResponseHeaders(req.Request.(*extproc.ProcessingRequest_ResponseHeaders), currentRequest)

	case *extproc.ProcessingRequest_ResponseBody:
		r := req.Request.(*extproc.ProcessingRequest_ResponseBody)

		// Note: The end of stream bool value is not reliable
		// Sometimes it's not set to true even if there is no more data to process
		if r.ResponseBody.GetEndOfStream() {
			return nil, nil
		}

		// TODO: Handle response raw body in the WAF
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_ResponseBody{},
		}, nil

	case *extproc.ProcessingRequest_ResponseTrailers:
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestTrailers{},
		}, nil

	default:
		return nil, status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}
}

func ProcessRequestHeaders(ctx context.Context, req *extproc.ProcessingRequest_RequestHeaders, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, error) {
	log.Debug("external_processing: received request headers: %v\n", req.RequestHeaders)

	headers, envoyHeaders := separateEnvoyHeaders(req.RequestHeaders.GetHeaders().GetHeaders())

	// Create args
	host, scheme, path, method, err := verifyRequestHttp2RequestHeaders(envoyHeaders)
	if err != nil {
		return nil, err
	}

	requestURI := scheme + "://" + host + path
	parsedUrl, err := url.Parse(requestURI)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Error parsing request URI: %v", err)
	}
	currentRequest.parsedUrl = parsedUrl

	// client ip set in the x-forwarded-for header (cf: https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_conn_man/headers#x-forwarded-for)
	ipTags, _ := httpsec2.ClientIPTags(headers, true, currentRequest.remoteAddr)

	currentRequest.requestArgs = httpsec.MakeHandlerOperationArgs(headers, method, host, currentRequest.remoteAddr, parsedUrl)
	headers = currentRequest.requestArgs.Headers // Replace headers with the ones from the args because it has been modified

	// Create span
	currentRequest.span = createExternalProcessedSpan(ctx, headers, method, host, path, currentRequest.remoteAddr, ipTags, parsedUrl)

	// Run WAF on request data
	currentRequest.op, currentRequest.blockAction, _ = httpsec.StartOperation(ctx, currentRequest.requestArgs)

	// Block handling: If triggered, we need to block the request, return an immediate response
	if blockPtr := currentRequest.blockAction.Load(); blockPtr != nil {
		response := doBlockRequest(currentRequest, blockPtr, headers)
		return response, nil
	}

	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extproc.HeadersResponse{
				Response: &extproc.CommonResponse{
					Status: extproc.CommonResponse_CONTINUE,
				},
			},
		},
	}, nil
}

// Verify the required HTTP2 headers are present
// Some mandatory headers need to be set. It can happen when it wasn't a real HTTP2 request sent by Envoy,
func verifyRequestHttp2RequestHeaders(headers map[string][]string) (string, string, string, string, error) {
	// :authority, :scheme, :path, :method

	for _, header := range []string{":authority", ":scheme", ":path", ":method"} {
		if _, ok := headers[header]; !ok {
			return "", "", "", "", status.Errorf(codes.InvalidArgument, "Missing required header: %v", header)
		}
	}

	return headers[":authority"][0], headers[":scheme"][0], headers[":path"][0], headers[":method"][0], nil
}

func verifyRequestHttp2ResponseHeaders(headers map[string][]string) (string, error) {
	// :status

	if _, ok := headers[":status"]; !ok {
		return "", status.Errorf(codes.InvalidArgument, "Missing required header: %v", ":status")
	}

	return headers[":status"][0], nil
}

func ProcessResponseHeaders(res *extproc.ProcessingRequest_ResponseHeaders, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, error) {
	log.Debug("external_processing: received response headers: %v\n", res.ResponseHeaders)

	headers, envoyHeaders := separateEnvoyHeaders(res.ResponseHeaders.GetHeaders().GetHeaders())

	statusCodeStr, err := verifyRequestHttp2ResponseHeaders(envoyHeaders)
	if err != nil {
		return nil, err
	}

	currentRequest.statusCode, err = strconv.Atoi(statusCodeStr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Error parsing response header status code: %v", err)
	}

	args := httpsec.HandlerOperationRes{
		Headers:    headers,
		StatusCode: currentRequest.statusCode,
	}

	currentRequest.op.Finish(args, currentRequest.span)
	currentRequest.op = nil

	// Block handling: If triggered, we need to block the request, return an immediate response
	if blockPtr := currentRequest.blockAction.Load(); blockPtr != nil {
		return doBlockRequest(currentRequest, blockPtr, headers), nil
	}

	httpsec2.SetResponseHeadersTags(currentRequest.span, headers)

	// Note: (cf. comment in the stream error handling)
	// The end of stream bool value is not reliable
	if res.ResponseHeaders.GetEndOfStream() {
		return nil, nil
	}

	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extproc.HeadersResponse{
				Response: &extproc.CommonResponse{
					Status: extproc.CommonResponse_CONTINUE,
				},
			},
		},
	}, nil
}

func createExternalProcessedSpan(ctx context.Context, headers map[string][]string, method string, host string, path string, remoteAddr string, ipTags map[string]string, parsedUrl *url.URL) tracer.Span {
	userAgent := ""
	if ua, ok := headers["User-Agent"]; ok {
		userAgent = ua[0]
	}

	span, _ := httptrace.StartHttpSpan(
		ctx,
		headers,
		host,
		method,
		httptrace.UrlFromUrl(parsedUrl),
		userAgent,
		remoteAddr,
		[]ddtrace.StartSpanOption{
			func(cfg *ddtrace.StartSpanConfig) {
				cfg.Tags[ext.ResourceName] = method + " " + path
				cfg.Tags[ext.SpanKind] = ext.SpanKindServer

				// Add client IP tags
				for k, v := range ipTags {
					cfg.Tags[k] = v
				}
			},
		}...,
	)

	httpsec2.SetRequestHeadersTags(span, headers)
	trace.SetAppsecStaticTags(span)

	return span
}

// Separate normal headers of the initial request made by the client and the pseudo headers of HTTP/2
// - Format the headers to be used by the tracer as a map[string][]string
// - Set header keys to be canonical
func separateEnvoyHeaders(receivedHeaders []*corev3.HeaderValue) (map[string][]string, map[string][]string) {
	headers := make(map[string][]string)
	pseudoHeadersHttp2 := make(map[string][]string)
	for _, v := range receivedHeaders {
		key := v.GetKey()
		if key[0] == ':' {
			pseudoHeadersHttp2[key] = []string{string(v.GetRawValue())}
		} else {
			headers[http.CanonicalHeaderKey(key)] = []string{string(v.GetRawValue())}
		}
	}
	return headers, pseudoHeadersHttp2
}

func doBlockRequest(currentRequest *CurrentRequest, blockAction *actions.BlockHTTP, headers map[string][]string) *extproc.ProcessingResponse {
	currentRequest.blocked = true

	var headerToSet map[string][]string
	var body []byte
	if blockAction.RedirectLocation != "" {
		headerToSet, body = actions.HandleRedirectLocationString(
			currentRequest.parsedUrl.Path,
			blockAction.RedirectLocation,
			blockAction.StatusCode,
			currentRequest.requestArgs.Method,
			currentRequest.requestArgs.Headers,
		)
	} else {
		headerToSet, body = blockAction.BlockingTemplate(headers)
	}

	var headersMutation []*v3.HeaderValueOption
	for k, v := range headerToSet {
		headersMutation = append(headersMutation, &v3.HeaderValueOption{
			Header: &v3.HeaderValue{
				Key:      k,
				RawValue: []byte(strings.Join(v, ",")),
			},
		})
	}

	httpsec2.SetResponseHeadersTags(currentRequest.span, headerToSet)
	currentRequest.statusCode = blockAction.StatusCode

	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extproc.ImmediateResponse{
				Status: &v32.HttpStatus{
					Code: v32.StatusCode(currentRequest.statusCode),
				},
				Headers: &extproc.HeaderMutation{
					SetHeaders: headersMutation,
				},
				Body: body,
				GrpcStatus: &extproc.GrpcStatus{
					Status: 0,
				},
			},
		},
	}
}

func closeSpan(currentRequest *CurrentRequest) {
	span := currentRequest.span
	if span != nil {
		// Finish the operation: it can be not finished when the request has been blocked or if an error occurred
		// > The response hasn't been processed
		if currentRequest.op != nil {
			currentRequest.op.Finish(httpsec.HandlerOperationRes{}, span)
			currentRequest.op = nil
		}

		// Note: The status code could be 0 if an internal error occurred
		statusCodeStr := strconv.Itoa(currentRequest.statusCode)
		span.SetTag(ext.HTTPCode, statusCodeStr)

		span.Finish()

		log.Debug("external_processing: span closed with status code: %v\n", currentRequest.statusCode)
		currentRequest.span = nil
	}
}
