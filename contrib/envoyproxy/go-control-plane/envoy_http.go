// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyextprocfilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func continueActionFunc(ctx context.Context, options proxy.ContinueActionOptions) error {
	if len(options.HeaderMutations) > 0 || options.Body {
		return buildHeadersResponse(ctx, options)
	}

	emptyResp, err := buildEmptyContinueResponse(options)
	if err != nil {
		return err
	}

	return sendResponse(ctx, &emptyResp)
}

func buildEmptyContinueResponse(options proxy.ContinueActionOptions) (envoyextproc.ProcessingResponse, error) {
	common := &envoyextproc.CommonResponse{
		Status: envoyextproc.CommonResponse_CONTINUE,
	}

	switch options.MessageType {
	case proxy.MessageTypeRequestHeaders:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_RequestHeaders{RequestHeaders: &envoyextproc.HeadersResponse{Response: common}}}, nil
	case proxy.MessageTypeRequestBody:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_RequestBody{RequestBody: &envoyextproc.BodyResponse{Response: common}}}, nil
	case proxy.MessageTypeResponseHeaders:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_ResponseHeaders{ResponseHeaders: &envoyextproc.HeadersResponse{Response: common}}}, nil
	case proxy.MessageTypeResponseBody:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_ResponseBody{ResponseBody: &envoyextproc.BodyResponse{Response: common}}}, nil
	case proxy.MessageTypeRequestTrailers:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_RequestTrailers{RequestTrailers: &envoyextproc.TrailersResponse{}}}, nil
	case proxy.MessageTypeResponseTrailers:
		return envoyextproc.ProcessingResponse{Response: &envoyextproc.ProcessingResponse_ResponseTrailers{ResponseTrailers: &envoyextproc.TrailersResponse{}}}, nil
	default:
		return envoyextproc.ProcessingResponse{}, status.Errorf(codes.Unknown, "Unknown request type: %v", options.MessageType)
	}
}

func blockActionFunc(ctx context.Context, data proxy.BlockActionOptions) error {
	blockedHeaders := convertHeadersToEnvoy(data.Headers)

	var statusCode int32
	if data.StatusCode > 0 && data.StatusCode <= math.MaxInt32 {
		statusCode = int32(data.StatusCode)
	}

	resp := envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &envoyextproc.ImmediateResponse{
				Status: &envoytypes.HttpStatus{
					Code: envoytypes.StatusCode(statusCode),
				},
				Headers: &envoyextproc.HeaderMutation{
					SetHeaders: blockedHeaders,
				},
				Body: data.Body,
				GrpcStatus: &envoyextproc.GrpcStatus{
					Status: 0,
				},
			},
		},
	}

	return sendResponse(ctx, &resp)
}

// buildHeadersResponse creates an Envoy HeadersResponse from provided data answering a RequestHeaders or ResponseHeaders message
func buildHeadersResponse(ctx context.Context, data proxy.ContinueActionOptions) error {
	var modeOverride *envoyextprocfilter.ProcessingMode
	if data.Body {
		modeOverride = &envoyextprocfilter.ProcessingMode{RequestBodyMode: envoyextprocfilter.ProcessingMode_STREAMED}
	}

	processingResponse := envoyextproc.ProcessingResponse{ModeOverride: modeOverride}
	headersResponse := &envoyextproc.HeadersResponse{
		Response: &envoyextproc.CommonResponse{
			Status: envoyextproc.CommonResponse_CONTINUE,
			HeaderMutation: &envoyextproc.HeaderMutation{
				SetHeaders: convertHeadersToEnvoy(data.HeaderMutations),
			},
		},
	}

	if data.MessageType == proxy.MessageTypeRequestHeaders {
		processingResponse.Response = &envoyextproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: headersResponse,
		}
	} else {
		processingResponse.Response = &envoyextproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: headersResponse,
		}
	}

	return sendResponse(ctx, &processingResponse)
}

// sendResponse sends a processing response back to Envoy
func sendResponse(ctx context.Context, response *envoyextproc.ProcessingResponse) error {
	instr.Logger().Debug("external_processing: sending response: %v\n", response)

	processServer, _ := ctx.Value(processServerKey).(envoyextproc.ExternalProcessor_ProcessServer)
	if processServer == nil {
		return status.Errorf(codes.Unknown, "No gRPC stream available to send the response")
	}

	if err := processServer.SendMsg(response); err != nil {
		instr.Logger().Error("external_processing: error sending response (probably because of an Envoy timeout): %s", err.Error())
		return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %s", err.Error())
	}

	return nil
}

// convertHeadersToEnvoy converts standard HTTP headers to Envoy HeaderValueOption format
func convertHeadersToEnvoy(headers http.Header) []*envoycore.HeaderValueOption {
	headerValueOptions := make([]*envoycore.HeaderValueOption, 0, len(headers))
	for k, v := range headers {
		headerValueOptions = append(headerValueOptions, &envoycore.HeaderValueOption{
			Header: &envoycore.HeaderValue{
				Key:      k,
				RawValue: []byte(strings.Join(v, ",")),
			},
		})
	}
	return headerValueOptions
}

// mergeMetadataHeaders merges the metadata headers of the grpc connection into the http headers of the request
// - Skip pseudo headers and headers that are already set
// - Set headers keys to be canonical
func mergeMetadataHeaders(md metadata.MD, headers http.Header) {
	for k, v := range md {
		if strings.HasPrefix(k, ":") {
			continue
		}

		// Skip the content-type header of the grpc request
		// Note: all envoy set headers are lower-case
		if k == "content-type" {
			continue
		}

		k = http.CanonicalHeaderKey(k)
		if _, ok := headers[k]; !ok {
			headers[k] = v
		}
	}
}

// splitPseudoHeaders splits normal headers of the initial request made by the client and the pseudo headers of HTTP/2
// - Format the headers to be used by the tracer as http.Header
// - Set headers keys to be canonical
func splitPseudoHeaders(receivedHeaders []*corev3.HeaderValue) (headers map[string][]string, pseudoHeaders map[string]string) {
	headers = make(map[string][]string, len(receivedHeaders)-4)
	pseudoHeaders = make(map[string]string, 4)
	for _, v := range receivedHeaders {
		key := v.GetKey()
		if key == "" {
			continue
		}
		if key[0] == ':' {
			pseudoHeaders[key] = string(v.GetRawValue())
			continue
		}

		canonKey := http.CanonicalHeaderKey(key)
		if headers[canonKey] == nil {
			headers[canonKey] = make([]string, 0, 1)
		}

		headers[canonKey] = append(headers[canonKey], string(v.GetRawValue()))
	}
	return headers, pseudoHeaders
}

// checkPseudoRequestHeaders Verify the required HTTP2 headers are present
// Some mandatory headers need to be set. It can happen when it wasn't a real HTTP2 request sent by Envoy,
func checkPseudoRequestHeaders(headers map[string]string) error {
	for _, header := range []string{":authority", ":scheme", ":path", ":method"} {
		if _, ok := headers[header]; !ok {
			return fmt.Errorf("missing required headers: %q", header)
		}
	}

	return nil
}

// checkPseudoResponseHeaders verifies the required HTTP2 headers are present
// Some mandatory headers need to be set. It can happen when it wasn't a real HTTP2 request sent by Envoy,
func checkPseudoResponseHeaders(headers map[string]string) error {
	if _, ok := headers[":status"]; !ok {
		return fmt.Errorf("missing required ':status' headers")
	}

	return nil
}

// getRemoteAddr extracts the remote address from the metadata headers of the gRPC stream
func getRemoteAddr(md metadata.MD) string {
	xfwd := md.Get("x-forwarded-for")
	length := len(xfwd)
	if length == 0 {
		return ""
	}

	// Get the first right value of x-forwarded-for headers
	// The rightmost IP address is the one that will be used as the remote client IP
	// https://datadoghq.atlassian.net/wiki/spaces/TS/pages/2766733526/Sensitive+IP+information#Where-does-the-value-of-the-http.client_ip-tag-come-from%3F
	return xfwd[length-1]
}
