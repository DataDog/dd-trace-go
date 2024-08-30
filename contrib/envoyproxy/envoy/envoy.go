// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package envoy

import (
	"context"
	"fmt"
	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"io"
	"log"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	v32 "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	httptrace2 "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
)

type CurrentRequest struct {
	op   *types.Operation
	span tracer.Span

	parsedUrl   *url.URL
	requestArgs types.HandlerOperationArgs

	blocked                 bool
	blockedHeaders          map[string][]string
	blockedTemplate         []byte
	blockedStatusCode       int
	blockedRedirectLocation string
}

func StreamServerInterceptor(opts ...grpctrace.Option) grpc.StreamServerInterceptor {
	interceptor := grpctrace.StreamServerInterceptor(opts...)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != "/envoy.service.ext_proc.v3.ExternalProcessor/Process" {
			return interceptor(srv, ss, info, handler)
		}

		ctx := ss.Context()
		currentRequest := &CurrentRequest{
			blocked: false,
		}

		defer func() {
			// Close the span if it's still open in case of an error
			closeSpan(currentRequest, 500)
		}()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			default:
			}

			var req extproc.ProcessingRequest
			err := ss.RecvMsg(&req)
			if err != nil {
				if err == io.EOF {
					return nil
				}

				fmt.Printf("Error receiving request/response: %v\n", err)
				return status.Errorf(codes.Unknown, "Error receiving request/response: %v", err)
			}

			resp, end, err := envoyExternalProcessingEventHandler(ctx, &req, currentRequest)
			if err != nil {
				fmt.Printf("Error processing request/response: %v\n", err)
				return status.Errorf(codes.Unknown, "Error processing request/response: %v", err)
			}

			// The end of the stream has been reached (no more data to process)
			if end {
				// Close the stream
				fmt.Println("End of stream reached")
				return nil
			}

			if err := ss.SendMsg(resp); err != nil {
				fmt.Printf("Error sending response: %v\n", err)
				return status.Errorf(codes.Unknown, "Error sending response: %v", err)
			}

			if currentRequest.blocked {
				fmt.Println("Request blocked, stream ended")
				return nil
			}
		}
	}
}

func envoyExternalProcessingEventHandler(ctx context.Context, req *extproc.ProcessingRequest, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, bool, error) {
	switch v := req.Request.(type) {
	case *extproc.ProcessingRequest_RequestHeaders:
		cr, err := ProcessRequestHeaders(ctx, req.Request.(*extproc.ProcessingRequest_RequestHeaders), currentRequest)
		return cr, false, err

	case *extproc.ProcessingRequest_RequestBody:
		// TODO: Handle request body in the WAF
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestBody{
				RequestBody: &extproc.BodyResponse{
					Response: &extproc.CommonResponse{
						Status: extproc.CommonResponse_CONTINUE,
					},
				},
			},
		}, false, nil

	case *extproc.ProcessingRequest_RequestTrailers:
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestTrailers{},
		}, false, nil

	case *extproc.ProcessingRequest_ResponseHeaders:
		return ProcessResponseHeaders(req.Request.(*extproc.ProcessingRequest_ResponseHeaders), currentRequest)

	case *extproc.ProcessingRequest_ResponseBody:
		// TODO: Handle response body in the WAF
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_ResponseBody{},
		}, true, nil

	case *extproc.ProcessingRequest_ResponseTrailers:
		return &extproc.ProcessingResponse{
			Response: &extproc.ProcessingResponse_RequestTrailers{},
		}, true, nil

	default:
		return nil, false, status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}
}

func ProcessRequestHeaders(ctx context.Context, req *extproc.ProcessingRequest_RequestHeaders, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, error) {
	log.Printf("Received request headers: %v\n", req.RequestHeaders)

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
	ipTags, clientIp := httptrace2.ClientIPTags(headers, false, "")

	currentRequest.requestArgs = httpsec.MakeHandlerOperationArgs(headers, method, host, clientIp, parsedUrl)
	headers = currentRequest.requestArgs.Headers // Replace headers with the ones from the args because it has been modified

	// Create span
	currentRequest.span = createExternalProcessedSpan(ctx, headers, method, host, path, ipTags, parsedUrl)

	// Run WAF on request data
	_, currentRequest.op = httpsec.StartOperation(ctx, currentRequest.requestArgs, func(op *types.Operation) {
		dyngo.OnData(op, func(a *sharedsec.HTTPAction) {
			// HTTP Blocking Action Handler
			currentRequest.blocked = a.Blocking()
			currentRequest.blockedStatusCode = a.StatusCode
			if a.RedirectLocation != "" {
				currentRequest.blockedRedirectLocation = a.RedirectLocation
			} else {
				currentRequest.blockedHeaders, currentRequest.blockedTemplate = a.BlockingTemplate(headers)
			}
		})
	})

	// Link Appsec events
	httptrace2.SetSecurityEventsTags(currentRequest.span, currentRequest.op.Events())

	// We need to block the request, return an immediate response
	if currentRequest.blocked {
		return doBlockRequest(currentRequest), nil
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

// TODO: Add check on "end_of_stream" to know if it's the last part of the request (without body) and close the stream without error (add a new return bool)
func ProcessResponseHeaders(res *extproc.ProcessingRequest_ResponseHeaders, currentRequest *CurrentRequest) (*extproc.ProcessingResponse, bool, error) {
	log.Printf("Received response headers: %v\n", res.ResponseHeaders)

	headers, envoyHeaders := separateEnvoyHeaders(res.ResponseHeaders.GetHeaders().GetHeaders())

	statusCodeStr, err := verifyRequestHttp2ResponseHeaders(envoyHeaders)
	if err != nil {
		return nil, false, err
	}

	statusCode, err := strconv.Atoi(statusCodeStr)
	if err != nil {
		return nil, false, status.Errorf(codes.InvalidArgument, "Error parsing response header status code: %v", err)
	}

	args := types.HandlerOperationRes{
		Headers: headers,
		Status:  statusCode,
	}

	secEvents := currentRequest.op.Finish(args)
	httptrace2.SetSecurityEventsTags(currentRequest.span, secEvents)

	// We need to block the request, return an immediate response
	if currentRequest.blocked {
		return doBlockRequest(currentRequest), false, nil
	}

	// Close the span
	httpsec.SetResponseHeadersTags(currentRequest.span, headers)
	closeSpan(currentRequest, statusCode)

	if res.ResponseHeaders.GetEndOfStream() {
		return &extproc.ProcessingResponse{}, true, nil
	}

	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extproc.HeadersResponse{
				Response: &extproc.CommonResponse{
					Status: extproc.CommonResponse_CONTINUE,
				},
			},
		},
	}, false, nil
}

func createExternalProcessedSpan(ctx context.Context, headers map[string][]string, method string, host string, path string, ipTags map[string]string, parsedUrl *url.URL) tracer.Span {
	userAgent := ""
	if ua, ok := headers["user-agent"]; ok {
		userAgent = ua[0]
	}

	span, _ := httptrace.StartHttpSpan(
		ctx,
		headers,
		host,
		method,
		httptrace.UrlFromUrl(parsedUrl),
		userAgent,
		"",
		[]ddtrace.StartSpanOption{
			func(cfg *ddtrace.StartSpanConfig) {
				cfg.Tags[ext.ResourceName] = method + " " + path
				cfg.Tags[ext.HTTPRoute] = path
				cfg.Tags[ext.SpanKind] = ext.SpanKindServer

				// Add client IP tags
				for k, v := range ipTags {
					cfg.Tags[k] = v
				}
			},
		}...,
	)

	httpsec.SetRequestHeadersTags(span, headers)
	trace.SetAppSecEnabledTags(span)

	return span
}

// Separate normal headers of the initial request made by the client and the pseudo headers of HTTP/2
// Also format the headers to be used by the tracer as a map[string][]string
func separateEnvoyHeaders(receivedHeaders []*corev3.HeaderValue) (map[string][]string, map[string][]string) {
	headers := make(map[string][]string)
	pseudoHeadersHttp2 := make(map[string][]string)
	for _, v := range receivedHeaders {
		key := strings.ToLower(v.GetKey())
		if key[0] == ':' {
			pseudoHeadersHttp2[key] = []string{string(v.GetRawValue())}
		} else {
			headers[key] = []string{string(v.GetRawValue())}
		}
	}
	return headers, pseudoHeadersHttp2
}

func doBlockRequest(currentRequest *CurrentRequest) *extproc.ProcessingResponse {
	trace.SetTags(currentRequest.span, map[string]interface{}{trace.BlockedRequestTag: true})

	var headerToSet map[string][]string
	var body []byte
	if currentRequest.blockedRedirectLocation != "" {
		headerToSet, body = sharedsec.HandleRedirectLocationString(
			currentRequest.parsedUrl.Path,
			currentRequest.blockedRedirectLocation,
			currentRequest.blockedStatusCode,
			currentRequest.requestArgs.Method,
			currentRequest.requestArgs.Headers,
		)
	} else {
		headerToSet = currentRequest.blockedHeaders
		body = currentRequest.blockedTemplate
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

	httpsec.SetResponseHeadersTags(currentRequest.span, headerToSet)
	closeSpan(currentRequest, currentRequest.blockedStatusCode)

	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extproc.ImmediateResponse{
				Status: &v32.HttpStatus{
					Code: v32.StatusCode(currentRequest.blockedStatusCode),
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

func closeSpan(currentRequest *CurrentRequest, statusCode int) {
	if currentRequest.span != nil {
		httptrace.FinishRequestSpan(currentRequest.span, statusCode)
		fmt.Printf("Span closed with status code: %v\n", statusCode)
		currentRequest.span = nil
	}
}
