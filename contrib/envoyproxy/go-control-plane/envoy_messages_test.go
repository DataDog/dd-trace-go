// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"testing"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
)

// The namespace and attribute names below are intentionally written as literals
// rather than referencing the constants used by the implementation: they encode
// the ext_proc wire contract that Envoy and GCP Service Extensions produce, so
// the test must fail if the implementation ever drifts away from it.
const (
	testExtProcAttributesNamespace = "envoy.filters.http.ext_proc"
	testSourceIPAttribute          = "source.ip"
	testSourceAddressAttribute     = "source.address"
)

// testSourceIPAttributes builds the ext_proc attributes map carrying source.ip.
// A nil value produces a namespace whose source.ip field is explicitly nil,
// which is distinct from the field being absent entirely.
func testSourceIPAttributes(value *structpb.Value) map[string]*structpb.Struct {
	return testExtProcAttributes(map[string]*structpb.Value{testSourceIPAttribute: value})
}

// testExtProcAttributes builds the ext_proc attributes map with arbitrary fields,
// for the cases that need something other than a lone source.ip.
func testExtProcAttributes(fields map[string]*structpb.Value) map[string]*structpb.Struct {
	return map[string]*structpb.Struct{
		testExtProcAttributesNamespace: {Fields: fields},
	}
}

// TestExtractRequestSourceIP pins the client-IP resolution contract for the GCP
// Service Extension integration: a trusted source.ip attribute is prepended to
// X-Forwarded-For (so the client-IP resolver, which selects the first global
// address scanning left to right, picks it over any client-supplied entry) and
// also becomes RemoteAddr. Every other case must leave the request untouched.
func TestExtractRequestSourceIP(t *testing.T) {
	const (
		clientXFF   = "9.9.9.9"
		customValue = "keep  me"
	)

	tests := []struct {
		name           string
		integration    Integration
		metadata       metadata.MD
		attributes     map[string]*structpb.Struct
		requestHeaders map[string]string
		wantXFF        []string
		wantRemoteAddr string
	}{
		// --- S1: happy path -------------------------------------------------
		{
			name:           "global source.ip with no client XFF",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("111.222.111.222")),
			wantXFF:        []string{"111.222.111.222"},
			wantRemoteAddr: "111.222.111.222",
		},
		// --- S5a: WAF-bypass guard: the client value is PRESERVED ------------
		{
			name:           "global source.ip is prepended and preserves client XFF",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{"18.18.18.18", clientXFF},
			wantRemoteAddr: "18.18.18.18",
		},
		// --- S7: private source.ip is still surfaced by this layer -----------
		// The downstream resolver may still prefer a forged global entry; that
		// is the documented, pre-existing gap and is not this layer's concern.
		{
			name:           "private source.ip is still prepended",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("192.168.0.9")),
			requestHeaders: map[string]string{"X-Forwarded-For": "1.1.1.1"},
			wantXFF:        []string{"192.168.0.9", "1.1.1.1"},
			wantRemoteAddr: "192.168.0.9",
		},
		// Google Cloud sends a single X-Forwarded-For holding
		// <client-supplied>,<client-ip>,<load-balancer-ip>. The client-supplied
		// part comes first, which is exactly the entry client IP resolution would
		// otherwise settle on.
		// https://cloud.google.com/load-balancing/docs/https#x-forwarded-for_header
		{
			name:           "GCLB-shaped X-Forwarded-For keeps its forged leading entry",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("203.0.113.50")),
			requestHeaders: map[string]string{"X-Forwarded-For": "1.1.1.1, 203.0.113.50, 35.191.10.1"},
			wantXFF:        []string{"203.0.113.50", "1.1.1.1, 203.0.113.50, 35.191.10.1"},
			wantRemoteAddr: "203.0.113.50",
		},
		// source.address is Envoy's own connection attribute and carries host:port.
		// Unlike source.ip it exists in stock Envoy, so it is the value that
		// actually arrives when the deployment does not extend the attribute set.
		{
			name:        "source.address is used when source.ip is absent",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewStringValue("203.0.113.77:57360"),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{"203.0.113.77", clientXFF},
			wantRemoteAddr: "203.0.113.77",
		},
		{
			name:        "bracketed IPv6 source.address is unwrapped",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewStringValue("[2001:db8::1]:443"),
			}),
			wantXFF:        []string{"2001:db8::1"},
			wantRemoteAddr: "2001:db8::1",
		},
		{
			name:        "source.address without a port still works",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewStringValue("203.0.113.77"),
			}),
			wantXFF:        []string{"203.0.113.77"},
			wantRemoteAddr: "203.0.113.77",
		},
		{
			name:        "source.ip takes precedence over source.address",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceIPAttribute:      structpb.NewStringValue("18.18.18.18"),
				testSourceAddressAttribute: structpb.NewStringValue("203.0.113.77:57360"),
			}),
			wantXFF:        []string{"18.18.18.18"},
			wantRemoteAddr: "18.18.18.18",
		},
		{
			name:        "malformed source.address changes nothing",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewStringValue("not-an-address"),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:        "non-string source.address changes nothing",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewNumberValue(57360),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:        "source.address is ignored for a non-GCP effective component",
			integration: EnvoyIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceAddressAttribute: structpb.NewStringValue("203.0.113.77:57360"),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		// --- canonicalisation ------------------------------------------------
		{
			name:           "IPv4-mapped IPv6 source.ip is unmapped",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("::ffff:203.0.113.11")),
			wantXFF:        []string{"203.0.113.11"},
			wantRemoteAddr: "203.0.113.11",
		},
		{
			name:           "expanded IPv6 source.ip is canonicalised",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("2001:0db8:0000:0000:0000:0000:0000:0001")),
			wantXFF:        []string{"2001:db8::1"},
			wantRemoteAddr: "2001:db8::1",
		},
		// --- S2: present but unusable -> change NOTHING -----------------------
		{
			name:           "malformed source.ip changes nothing",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("not-an-ip")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "zoned source.ip changes nothing",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("fe80::1%eth0")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "non-string source.ip changes nothing",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewNumberValue(42)),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "nil source.ip value changes nothing",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(nil),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		// --- S3: absent -> change NOTHING ------------------------------------
		{
			name:           "namespace without source.ip field changes nothing",
			integration:    GCPServiceExtensionIntegration,
			attributes:     map[string]*structpb.Struct{testExtProcAttributesNamespace: {}},
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "no attributes at all changes nothing",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "absent source.ip still falls back to metadata remote addr",
			integration:    GCPServiceExtensionIntegration,
			metadata:       metadata.Pairs("x-forwarded-for", "192.0.2.20"),
			wantXFF:        []string{"192.0.2.20"},
			wantRemoteAddr: "192.0.2.20",
		},
		// --- S4: non-GCP effective component -> change NOTHING ---------------
		{
			name:           "envoy integration ignores source.ip",
			integration:    EnvoyIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("111.222.111.222")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "GCP downgraded to envoy by metadata ignores source.ip",
			integration:    GCPServiceExtensionIntegration,
			metadata:       metadata.Pairs(datadogEnvoyIntegrationHeader, "1"),
			attributes:     testSourceIPAttributes(structpb.NewStringValue("111.222.111.222")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
		{
			name:           "GCP downgraded to istio by metadata ignores source.ip",
			integration:    GCPServiceExtensionIntegration,
			metadata:       metadata.Pairs(datadogIntegrationHeader, "1"),
			attributes:     testSourceIPAttributes(structpb.NewStringValue("111.222.111.222")),
			requestHeaders: map[string]string{"X-Forwarded-For": clientXFF},
			wantXFF:        []string{clientXFF},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.metadata != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.metadata)
			}

			requestHeaders := map[string]string{"X-Custom": customValue}
			for k, v := range tc.requestHeaders {
				requestHeaders[k] = v
			}

			message := messageRequestHeaders{
				ProcessingRequest: &envoyextproc.ProcessingRequest{Attributes: tc.attributes},
				HttpHeaders: &envoyextproc.HttpHeaders{
					Headers: makeRequestHeaders(t, requestHeaders, "GET", "/"),
				},
				integration: tc.integration,
			}

			request, err := message.ExtractRequest(ctx)
			require.NoError(t, err)

			require.Equal(t, tc.wantXFF, request.Headers["X-Forwarded-For"],
				"X-Forwarded-For must carry the trusted source.ip first while preserving client-supplied entries")
			require.Equal(t, tc.wantRemoteAddr, request.RemoteAddr)

			// Unrelated headers must survive byte-for-byte so the WAF keeps
			// inspecting the full, unmodified header set.
			require.Equal(t, []string{customValue}, request.Headers["X-Custom"])
		})
	}
}

// TestExtractRequestSourceIPDoesNotDropHeaders guards against the tempting but
// unsafe implementation of stripping client-IP headers to win the resolution:
// removing X-Forwarded-For from the request would also remove it from the WAF's
// headers address, so an attack payload placed there would never be inspected.
func TestExtractRequestSourceIPDoesNotDropHeaders(t *testing.T) {
	const maliciousXFF = "' OR 1=1 --"

	message := messageRequestHeaders{
		ProcessingRequest: &envoyextproc.ProcessingRequest{
			Attributes: testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
		},
		HttpHeaders: &envoyextproc.HttpHeaders{
			Headers: makeRequestHeaders(t, map[string]string{"X-Forwarded-For": maliciousXFF}, "GET", "/"),
		},
		integration: GCPServiceExtensionIntegration,
	}

	request, err := message.ExtractRequest(context.Background())
	require.NoError(t, err)

	require.Contains(t, request.Headers["X-Forwarded-For"], maliciousXFF,
		"the client-supplied X-Forwarded-For must remain visible to the WAF")
	require.Equal(t, "18.18.18.18", request.RemoteAddr)

	var _ proxy.PseudoRequest = request
}
