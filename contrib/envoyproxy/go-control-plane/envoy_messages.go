// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
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
	// integrationDeclared reports whether integration was named by the caller
	// rather than defaulted to GCP. See trustedClientIP.
	integrationDeclared bool
}

func (m messageRequestHeaders) ExtractRequest(ctx context.Context) (proxy.PseudoRequest, error) {
	headers, pseudoHeaders := splitPseudoHeaders(m.GetHeaders().GetHeaders())
	if err := checkPseudoRequestHeaders(pseudoHeaders); err != nil {
		return proxy.PseudoRequest{}, err
	}

	// Captured before mergeMetadataHeaders, which fills an absent X-Forwarded-For
	// from the ext_proc stream's own metadata. The GCLB positional contract below
	// describes the request that travelled through the load balancer, not the gRPC
	// connection carrying this callout, so only the proxied request's own header
	// may decide identity.
	requestForwardedFor := headers["X-Forwarded-For"]

	var remoteAddr string
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		mergeMetadataHeaders(md, headers)
		remoteAddr = getRemoteAddr(md)
	}

	headers["Host"] = append(headers["Host"], pseudoHeaders[":authority"])

	// X-Forwarded-For is deliberately left exactly as received, forged entries
	// and all, so the WAF keeps inspecting the header the client actually sent.
	// The trustworthy address travels beside it instead.
	var remoteIP, clientIP netip.Addr
	if m.component(ctx) == componentNameGCPServiceExtension {
		if trustedIP, ok := trustedClientIP(m.ProcessingRequest.GetAttributes(), requestForwardedFor, m.integrationDeclared); ok {
			remoteIP, clientIP = trustedIP, trustedIP
		}
	}

	return proxy.PseudoRequest{
		Method:     pseudoHeaders[":method"],
		Authority:  pseudoHeaders[":authority"],
		Path:       pseudoHeaders[":path"],
		Scheme:     pseudoHeaders[":scheme"],
		Headers:    headers,
		RemoteAddr: remoteAddr,
		RemoteIP:   remoteIP,
		ClientIP:   clientIP,
	}, nil
}

const (
	// extProcAttributesNamespace is the key of the ProcessingRequest attributes
	// map under which Envoy publishes the attributes selected by the extension
	// configuration. Google Cloud Service Extensions spell that selection
	// forwardAttributes; the resulting wire format is Envoy's.
	//
	// Envoy keys that map by the filter's own name, so the value below is not
	// arbitrary and must not be "corrected" to something friendlier:
	// source/extensions/filters/http/ext_proc/ext_proc.cc does
	// (*req.mutable_attributes())[FilterName] = std::move(attributes).
	extProcAttributesNamespace = "envoy.filters.http.ext_proc"

	// sourceIPAttribute holds the address of the TCP peer as seen by the load
	// balancer. Unlike X-Forwarded-For it is set by the infrastructure rather
	// than the client, so it is authoritative whenever it is present.
	//
	// source.address, Envoy's own host:port spelling of the same thing, is NOT
	// accepted as a fallback: Google Cloud rejects it at configuration time
	// ("invalid forward attribute source.address"), so any code handling it
	// would be unreachable on the only path this is gated to.
	sourceIPAttribute = "source.ip"
)

// trustedClientIP returns the address of the peer the Google Cloud load balancer
// observed, which is the one address in the request the client could not forge.
//
// It is only meaningful for GCP Service Extensions and callers must gate on that.
//
// gclbShape additionally gates the positional X-Forwarded-For rule, which unlike
// source.ip is a property of Google Cloud's load balancer rather than of Envoy.
// Pass false unless the deployment positively declared itself a GCP Service
// Extension: an unset integration is defaulted to GCP, and a caller that named
// nothing is more likely a self-hosted Envoy, which appends a single
// X-Forwarded-For entry and would therefore have its len-2 position land on a
// client-supplied value.
func trustedClientIP(attributes map[string]*structpb.Struct, forwardedFor []string, gclbShape bool) (netip.Addr, bool) {
	// source.ip is set by the infrastructure and does not exist in stock Envoy,
	// so it is trustworthy wherever it turns up.
	if sourceIP, ok := parseExtProcSourceIP(attributes); ok {
		return sourceIP, true
	}
	if !gclbShape {
		return netip.Addr{}, false
	}
	return gclbForwardedForClientIP(forwardedFor)
}

// parseExtProcSourceIP returns the peer address published through the ext_proc
// attributes. It reports false unless the attribute holds an address we can
// interpret: a value we cannot read must leave client IP resolution exactly as
// it would have been, rather than silently substituting a wrong address.
func parseExtProcSourceIP(attributes map[string]*structpb.Struct) (netip.Addr, bool) {
	value := attributes[extProcAttributesNamespace].GetFields()[sourceIPAttribute]

	stringValue, ok := value.GetKind().(*structpb.Value_StringValue)
	if !ok {
		return netip.Addr{}, false
	}

	raw := stringValue.StringValue
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}

	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}

	// Collapse IPv4-mapped IPv6 so the value matches how the address would be
	// written anywhere else in the trace.
	return addr.Unmap(), true
}

// gclbForwardedForClientIP recovers the client address from the X-Forwarded-For
// shape a Google Cloud load balancer produces, so that identification works with
// no extension configuration at all.
//
// GCLB appends two entries of its own to whatever the client sent:
//
//	X-Forwarded-For: <client-supplied>,<client observed by GCLB>,<forwarding rule IP>
//
// so the second-to-last entry is the observed peer. Position is the whole
// contract here: the last entry is public on an external load balancer and
// private on an internal one, so it cannot be identified by address class. Stock
// Envoy appends a single entry, which is why this must never run outside GCP.
func gclbForwardedForClientIP(forwardedFor []string) (netip.Addr, bool) {
	entries := make([]string, 0, len(forwardedFor)+1)
	for _, value := range forwardedFor {
		for value != "" {
			var entry string
			entry, value, _ = strings.Cut(value, ",")
			entries = append(entries, strings.TrimSpace(entry))
		}
	}

	if len(entries) < 2 {
		// Nothing was appended by a load balancer we can recognise; leave
		// resolution to the default policy.
		return netip.Addr{}, false
	}

	addr, err := netip.ParseAddr(entries[len(entries)-2])
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}

	return addr.Unmap(), true
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
