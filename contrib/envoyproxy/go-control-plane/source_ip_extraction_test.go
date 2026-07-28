// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"net/netip"
	"testing"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
)

func TestMessageRequestHeadersExtractRequestSourceIP(t *testing.T) {
	legacyRemoteAddr := "192.0.2.20"
	rawXFF, rawCustom := " 198.51.100.42,  192.0.2.1 ", "value  with  spaces"
	tests := []struct {
		name           string
		integration    Integration
		metadata       metadata.MD
		attributes     map[string]*structpb.Struct
		wantResolution proxy.ClientIPResolution
		wantRemoteAddr string
	}{
		{"public IPv4", GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("203.0.113.10")), proxy.NewClientIPResolution(netip.MustParseAddr("203.0.113.10")), "203.0.113.10"},
		{"private IPv4", GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("10.20.30.40")), proxy.NewClientIPResolution(netip.MustParseAddr("10.20.30.40")), "10.20.30.40"},
		{"IPv6", GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("2001:0db8:0000:0000:0000:0000:0000:0001")), proxy.NewClientIPResolution(netip.MustParseAddr("2001:db8::1")), "2001:db8::1"},
		{"mapped address", GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("::ffff:203.0.113.11")), proxy.NewClientIPResolution(netip.MustParseAddr("203.0.113.11")), "203.0.113.11"},
		{"nil value", GCPServiceExtensionIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(nil), proxy.InvalidClientIPResolution(), ""},
		{"malformed string", GCPServiceExtensionIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(structpb.NewStringValue("not-an-ip")), proxy.InvalidClientIPResolution(), ""},
		{"scoped address", GCPServiceExtensionIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(structpb.NewStringValue("fe80::1%eth0")), proxy.InvalidClientIPResolution(), ""},
		{"non-string", GCPServiceExtensionIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(structpb.NewNumberValue(42)), proxy.InvalidClientIPResolution(), ""},
		{"missing field", GCPServiceExtensionIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), map[string]*structpb.Struct{gcpServiceExtensionAttributesNamespace: {}}, proxy.ClientIPResolution{}, legacyRemoteAddr},
		{"non-GCP", EnvoyIntegration, metadata.Pairs("x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(structpb.NewStringValue("203.0.113.12")), proxy.ClientIPResolution{}, legacyRemoteAddr},
		{"effective non-GCP", GCPServiceExtensionIntegration, metadata.Pairs(datadogEnvoyIntegrationHeader, "1", "x-forwarded-for", legacyRemoteAddr), sourceIPAttributes(structpb.NewStringValue("203.0.113.12")), proxy.ClientIPResolution{}, legacyRemoteAddr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.metadata != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.metadata)
			}
			message := messageRequestHeaders{
				ProcessingRequest: &envoyextproc.ProcessingRequest{Attributes: tc.attributes},
				HttpHeaders: &envoyextproc.HttpHeaders{Headers: makeRequestHeaders(t, map[string]string{
					"X-Forwarded-For": rawXFF, "X-Custom": rawCustom,
				}, "GET", "/")},
				integration: tc.integration,
			}
			request, err := message.ExtractRequest(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantResolution, request.ClientIP)
			require.Equal(t, tc.wantRemoteAddr, request.RemoteAddr)
			require.Equal(t, []string{rawXFF}, request.Headers["X-Forwarded-For"])
			require.Equal(t, []string{rawCustom}, request.Headers["X-Custom"])
		})
	}
}
