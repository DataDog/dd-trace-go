// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strconv"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/metadata"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
)

type requestHeadersEnvoy struct {
	req         *extproc.ProcessingRequest_RequestHeaders
	integration Integration
}

func (a *requestHeadersEnvoy) NewRequest(ctx context.Context) (*http.Request, error) {
	headers, pseudoHeaders := splitPseudoHeaders(a.req.RequestHeaders.GetHeaders().GetHeaders())
	if err := checkPseudoRequestHeaders(pseudoHeaders); err != nil {
		return nil, err
	}

	var remoteAddr string
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		mergeMetadataHeaders(md, headers)
		remoteAddr = getRemoteAddr(md)
	}

	var tlsState *tls.ConnectionState
	if pseudoHeaders[":scheme"] == "https" {
		tlsState = &tls.ConnectionState{}
	}

	headers["Host"] = append(headers["Host"], pseudoHeaders[":authority"])

	return message_processor.NewRequest(ctx, pseudoHeaders[":scheme"], pseudoHeaders[":authority"], pseudoHeaders[":path"], pseudoHeaders[":method"], headers, remoteAddr, tlsState)
}

func (a *requestHeadersEnvoy) EndOfStream() bool {
	return a.req.RequestHeaders.GetEndOfStream()
}

const (
	componentNameGCPServiceExtension = "gcp-service-extension"
	componentNameEnvoy               = "envoy"
	componentNameIstio               = "istio"

	datadogEnvoyIntegrationHeader = "x-datadog-envoy-integration"
	datadogIntegrationHeader      = "x-datadog-istio-integration"
)

func (i Integration) String() string {
	switch i {
	case GCPServiceExtensionIntegration:
		return componentNameGCPServiceExtension
	case EnvoyIntegration:
		return componentNameEnvoy
	case IstioIntegration:
		return componentNameIstio
	default:
		return componentNameGCPServiceExtension
	}
}

func (a *requestHeadersEnvoy) Component(ctx context.Context) string {
	// As the integration (callout container) is run by default with the GCP Service Extension value,
	// we can consider that if this flag is false, it means that it is running in a custom integration.
	if a.integration != GCPServiceExtensionIntegration {
		return a.integration.String()
	}

	// In newer version of the documentation, customers are instructed to inject the
	// Datadog integration header in their Envoy configuration to identify the integration.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		valuesEnvoy := md.Get(datadogEnvoyIntegrationHeader)
		if len(valuesEnvoy) > 0 && valuesEnvoy[0] == "1" {
			return componentNameEnvoy
		}

		valuesIstio := md.Get(datadogIntegrationHeader)
		if len(valuesIstio) > 0 && valuesIstio[0] == "1" {
			return componentNameIstio
		}
	}

	return componentNameGCPServiceExtension
}

func (a *requestHeadersEnvoy) Framework() string {
	return "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
}

type requestBodyEnvoy struct {
	req *extproc.ProcessingRequest_RequestBody
}

func (a *requestBodyEnvoy) Body() []byte {
	return a.req.RequestBody.GetBody()
}

func (a *requestBodyEnvoy) EndOfStream() bool {
	return a.req.RequestBody.GetEndOfStream()
}

type responseHeadersEnvoy struct {
	res *extproc.ProcessingRequest_ResponseHeaders
}

func (a *responseHeadersEnvoy) InitResponseWriter(w http.ResponseWriter) error {
	headers, pseudoHeaders := splitPseudoHeaders(a.res.ResponseHeaders.GetHeaders().GetHeaders())
	if err := checkPseudoResponseHeaders(pseudoHeaders); err != nil {
		return err
	}

	status, err := strconv.Atoi(pseudoHeaders[":status"])
	if err != nil {
		return fmt.Errorf("error parsing status code %q: %w", pseudoHeaders[":status"], err)
	}

	for k, v := range headers {
		w.Header()[k] = v
	}

	w.WriteHeader(status)
	return nil
}

func (a *responseHeadersEnvoy) EndOfStream() bool {
	return a.res.ResponseHeaders.GetEndOfStream()
}

type responseBodyEnvoy struct {
	res *extproc.ProcessingRequest_ResponseBody
}

func (a *responseBodyEnvoy) Body() []byte {
	return a.res.ResponseBody.GetBody()
}

func (a *responseBodyEnvoy) EndOfStream() bool {
	return a.res.ResponseBody.GetEndOfStream()
}
