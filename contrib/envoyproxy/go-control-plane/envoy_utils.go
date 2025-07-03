// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"math"
	"net/http"
	"strings"

	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

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

// buildImmediateResponse creates an Envoy immediate response for blocking scenarios
func buildImmediateResponse(writer *fakeResponseWriter) *envoyextproc.ProcessingResponse {
	headersMutation := convertHeadersToEnvoy(writer.headers)

	var statusCode int32
	if writer.status > 0 && writer.status <= math.MaxInt32 {
		statusCode = int32(writer.status)
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &envoyextproc.ImmediateResponse{
				Status: &envoytypes.HttpStatus{
					Code: envoytypes.StatusCode(statusCode),
				},
				Headers: &envoyextproc.HeaderMutation{
					SetHeaders: headersMutation,
				},
				Body: writer.body,
				GrpcStatus: &envoyextproc.GrpcStatus{
					Status: 0,
				},
			},
		},
	}
}
