// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"sync"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
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

	// The load balancer can forward the address of the TCP peer it observed as
	// the source.ip attribute, which the client cannot forge. Prepend it to
	// X-Forwarded-For: client IP resolution scans that header from the left and
	// stops at the first global address, so the trusted value wins over any
	// entry the client supplied. Client-supplied values are deliberately kept so
	// the WAF keeps inspecting the header exactly as it was received.
	if m.component(ctx) == componentNameGCPServiceExtension {
		if sourceIP, ok := parseExtProcSourceIP(m.ProcessingRequest.GetAttributes()); ok {
			addr := sourceIP.String()
			headers["X-Forwarded-For"] = append([]string{addr}, headers["X-Forwarded-For"]...)
			remoteAddr = addr
		}
	}

	return proxy.PseudoRequest{
		Method:     pseudoHeaders[":method"],
		Authority:  pseudoHeaders[":authority"],
		Path:       pseudoHeaders[":path"],
		Scheme:     pseudoHeaders[":scheme"],
		Headers:    headers,
		RemoteAddr: remoteAddr,
	}, nil
}

const (
	// extProcAttributesNamespace is the key of the ProcessingRequest attributes
	// map under which Envoy publishes the attributes selected by the extension
	// configuration. Google Cloud Service Extensions spell that selection
	// forwardAttributes; the resulting wire format is Envoy's.
	extProcAttributesNamespace = "envoy.filters.http.ext_proc"

	// sourceIPAttribute holds the address of the TCP peer as seen by the load
	// balancer. Unlike X-Forwarded-For it is set by the infrastructure rather
	// than the client, so it is authoritative whenever it is present.
	sourceIPAttribute = "source.ip"
)

// parseExtProcSourceIP returns the trusted client address published through the
// ext_proc source.ip attribute. It reports false unless the attribute is present
// and holds a single unzoned IP address: a value we cannot interpret must leave
// client IP resolution exactly as it would have been without it, rather than
// silently substituting a wrong address.
func parseExtProcSourceIP(attributes map[string]*structpb.Struct) (netip.Addr, bool) {
	value, found := attributes[extProcAttributesNamespace].GetFields()[sourceIPAttribute]
	if !found {
		return netip.Addr{}, false
	}

	// Anything other than a plain string is not something we know how to read.
	stringValue, ok := value.GetKind().(*structpb.Value_StringValue)
	if !ok {
		return netip.Addr{}, false
	}

	// source.ip carries a bare address; the port is a separate attribute.
	sourceIP, err := netip.ParseAddr(stringValue.StringValue)
	if err != nil || sourceIP.Zone() != "" {
		return netip.Addr{}, false
	}

	// Collapse IPv4-mapped IPv6 so the value matches how the address would be
	// written anywhere else in the trace.
	return sourceIP.Unmap(), true
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

func (m messageRequestHeaders) BodyParsingSizeLimit(ctx context.Context) int {
	switch m.component(ctx) {
	case componentNameGCPServiceExtension:
		return 0
	default:
		return proxy.DefaultBodyParsingSizeLimit
	}
}

func (m messageRequestHeaders) SpanOptions(ctx context.Context) []tracer.StartSpanOption {
	return []tracer.StartSpanOption{tracer.Tag(ext.Component, m.component(ctx))}
}

func (m messageRequestHeaders) component(ctx context.Context) string {
	// As the integration (callout container) is run by default with the GCP Service Extension value,
	// we can consider that if this flag is false, it means that it is running in a custom integration.
	if m.integration != GCPServiceExtensionIntegration {
		return m.integration.String()
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

		// We don't have the ability to add custom headers in envoy gateway EnvoyExtensionPolicy CRD.
		// So we fall back to detecting if we are running in k8s or not.
		// If we are running in k8s, we assume it is Envoy Gateway, otherwise GCP Service Extension.
		if isK8s() {
			return componentNameEnvoyGateway
		}
	}

	return componentNameGCPServiceExtension

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
