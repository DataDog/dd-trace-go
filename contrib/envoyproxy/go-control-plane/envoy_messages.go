// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/metadata"
)

var _ proxy.RequestHeaders = (*messageRequestHeaders)(nil)
var _ proxy.ResponseHeaders = (*responseHeadersEnvoy)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)
var _ proxy.HTTPBody = (*messageBody)(nil)

type messageRequestHeaders struct {
	*extproc.ProcessingRequest
	*extproc.HttpHeaders
	integration Integration
}

func (m messageRequestHeaders) ExtractRequest(ctx context.Context) (proxy.PseudoRequest, error) {
	headers, pseudoHeaders := splitPseudoHeaders(m.GetHeaders().GetHeaders())
	if err := checkPseudoRequestHeaders(pseudoHeaders); err != nil {
		return proxy.PseudoRequest{}, err
	}

	var remoteAddr string
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		mergeMetadataHeaders(md, headers)
		remoteAddr = getRemoteAddr(md)
	}

	headers["Host"] = append(headers["Host"], pseudoHeaders[":authority"])
	return proxy.PseudoRequest{
		Method:     pseudoHeaders[":method"],
		Authority:  pseudoHeaders[":authority"],
		Path:       pseudoHeaders[":path"],
		Scheme:     pseudoHeaders[":scheme"],
		Headers:    headers,
		RemoteAddr: remoteAddr,
	}, nil
}

func (m messageRequestHeaders) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestHeaders
}

const (
	componentNameGCPServiceExtension = "gcp-service-extension"
	componentNameEnvoy               = "envoy"
	componentNameEnvoyGateway        = "envoy-gateway"
	componentNameIstio               = "istio"

	datadogEnvoyIntegrationHeader = "x-datadog-envoy-integration"
	datadogIntegrationHeader      = "x-datadog-istio-integration"
)

var isK8s = sync.OnceValue(func() bool {
	return os.Getenv("KUBERNETES") != ""
})

func (i Integration) String() string {
	switch i {
	case GCPServiceExtensionIntegration:
		return componentNameGCPServiceExtension
	case EnvoyIntegration:
		return componentNameEnvoy
	case EnvoyGatewayIntegration:
		return componentNameEnvoyGateway
	case IstioIntegration:
		return componentNameIstio
	default:
		return componentNameGCPServiceExtension
	}
}

func (m messageRequestHeaders) SpanOptions(ctx context.Context) []tracer.StartSpanOption {
	// As the integration (callout container) is run by default with the GCP Service Extension value,
	// we can consider that if this flag is false, it means that it is running in a custom integration.
	if m.integration != GCPServiceExtensionIntegration {
		return []tracer.StartSpanOption{tracer.Tag(ext.Component, m.integration.String())}
	}

	// In newer version of the documentation, customers are instructed to inject the
	// Datadog integration header in their Envoy configuration to identify the integration.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		valuesEnvoy := md.Get(datadogEnvoyIntegrationHeader)
		if len(valuesEnvoy) > 0 && valuesEnvoy[0] == "1" {
			return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameEnvoy)}
		}

		valuesIstio := md.Get(datadogIntegrationHeader)
		if len(valuesIstio) > 0 && valuesIstio[0] == "1" {
			return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameIstio)}
		}

		// We don't have the ability to add custom headers in envoy gateway EnvoyExtensionPolicy CRD.
		// So we fall back to detecting if we are running in k8s or not.
		// If we are running in k8s, we assume it is Envoy Gateway, otherwise GCP Service Extension.
		if isK8s() {
			return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameEnvoyGateway)}
		}
	}

	return []tracer.StartSpanOption{tracer.Tag(ext.Component, componentNameGCPServiceExtension)}
}

type responseHeadersEnvoy struct {
	*extproc.ProcessingRequest
	*extproc.HttpHeaders
}

func (m responseHeadersEnvoy) ExtractResponse() (proxy.PseudoResponse, error) {
	headers, pseudoHeaders := splitPseudoHeaders(m.GetHeaders().GetHeaders())
	if err := checkPseudoResponseHeaders(pseudoHeaders); err != nil {
		return proxy.PseudoResponse{}, err
	}

	status, err := strconv.Atoi(pseudoHeaders[":status"])
	if err != nil {
		return proxy.PseudoResponse{}, fmt.Errorf("error parsing status code %q: %w", pseudoHeaders[":status"], err)
	}

	return proxy.PseudoResponse{
		StatusCode: status,
		Headers:    headers,
	}, nil
}

func (m responseHeadersEnvoy) MessageType() proxy.MessageType {
	return proxy.MessageTypeResponseHeaders
}

type messageBody struct {
	*extproc.ProcessingRequest
	*extproc.HttpBody
	m proxy.MessageType
}

func (m messageBody) MessageType() proxy.MessageType {
	return m.m
}
