// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

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
	integration            Integration
	trustGCLBXForwardedFor bool
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
	var clientIP netip.Addr
	if m.component(ctx) == componentNameGCPServiceExtension {
		if trustedIP, ok := trustedClientIP(m.ProcessingRequest.GetAttributes(), requestForwardedFor, m.trustGCLBXForwardedFor); ok {
			clientIP = trustedIP
		}
	}

	return proxy.PseudoRequest{
		Method:     pseudoHeaders[":method"],
		Authority:  pseudoHeaders[":authority"],
		Path:       pseudoHeaders[":path"],
		Scheme:     pseudoHeaders[":scheme"],
		Headers:    headers,
		RemoteAddr: remoteAddr,
		ClientIP:   clientIP,
	}, nil
}

const (
	// Envoy keys forwarded attributes by the filter's own name.
	extProcAttributesNamespace = "envoy.filters.http.ext_proc"

	// GCP rejects source.address as a forward attribute, so source.ip is the only
	// infrastructure-provided address accepted here.
	sourceIPAttribute = "source.ip"
)

func trustedClientIP(attributes map[string]*structpb.Struct, forwardedFor []string, gclbShape bool) (netip.Addr, bool) {
	if sourceIP, ok := parseExtProcSourceIP(attributes); ok {
		return sourceIP, true
	}
	if !gclbShape {
		return netip.Addr{}, false
	}
	return gclbForwardedForClientIP(forwardedFor)
}

func parseExtProcSourceIP(attributes map[string]*structpb.Struct) (netip.Addr, bool) {
	raw := attributes[extProcAttributesNamespace].GetFields()[sourceIPAttribute].GetStringValue()
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}

	return addr.Unmap(), true
}

// GCLB appends two entries of its own to whatever the client sent:
//
//	X-Forwarded-For: <client-supplied>,<client observed by GCLB>,<forwarding rule IP>
//
// Position, not address class, identifies the observed peer: the forwarding
// rule may be public or private.
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

// AckBodyMessagesUntilEndOfStream reports whether the gateway requires an acknowledgement
// for every response body message. Only Google Cloud load balancers do; self-managed Envoy,
// Istio and Envoy Gateway accept a clean close as soon as the analysis is done.
//
// The gateway is only identifiable per request, because the published callout container
// reports GCPServiceExtensionIntegration for every TCP deployment. Draining therefore
// stops only on positive identification — an explicitly configured integration or the
// documented header — and never on inference. Getting this wrong reintroduces the callout
// timeouts the acknowledgement exists to prevent, whereas acknowledging a gateway that did
// not need it only costs a round trip per chunk.
func (m messageRequestHeaders) AckBodyMessagesUntilEndOfStream(ctx context.Context) bool {
	// Explicitly configured as something other than Google Cloud.
	if m.integration != GCPServiceExtensionIntegration {
		return false
	}

	// Explicitly identified by the header the documentation instructs customers to inject.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(datadogEnvoyIntegrationHeader); len(values) > 0 && values[0] == "1" {
			return false
		}
		if values := md.Get(datadogIntegrationHeader); len(values) > 0 && values[0] == "1" {
			return false
		}
	}

	return true
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
	}

	// Envoy Gateway cannot inject a header from its EnvoyExtensionPolicy CRD, so it has to
	// be named explicitly through the integration above. Presence in Kubernetes used to be
	// taken as proof of Envoy Gateway, which cannot work: a Google Cloud Service Extensions
	// callout deployed on GKE is equally in Kubernetes, so the two are indistinguishable by
	// that signal.
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
