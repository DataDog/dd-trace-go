// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"net/netip"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

// The namespace and attribute names below are intentionally written as literals
// rather than referencing the constants used by the implementation: they encode
// the ext_proc wire contract that Envoy and GCP Service Extensions produce, so
// the test must fail if the implementation ever drifts away from it.
const (
	testExtProcAttributesNamespace = "envoy.filters.http.ext_proc"
	testSourceIPAttribute          = "source.ip"
)

func testExtProcAttributes(fields map[string]*structpb.Value) map[string]*structpb.Struct {
	return map[string]*structpb.Struct{
		testExtProcAttributesNamespace: {Fields: fields},
	}
}

func testSourceIPAttributes(value *structpb.Value) map[string]*structpb.Struct {
	return testExtProcAttributes(map[string]*structpb.Value{testSourceIPAttribute: value})
}

// clientIPTestCase drives ExtractRequest and states which client identity the
// integration must hand across the instrumentation boundary. A zero wantClientIP
// means "resolve nothing here", which defers to the default resolution policy
// and therefore reproduces the behaviour that predates this integration hook.
type clientIPTestCase struct {
	name        string
	integration Integration
	// undeclaredIntegration reproduces an embedder that left
	// AppsecEnvoyConfig.Integration unset and was therefore defaulted to GCP.
	undeclaredIntegration bool
	metadata              metadata.MD
	attributes            map[string]*structpb.Struct
	requestHeaders        map[string]string
	wantClientIP          string
}

func clientIPTestCases() []clientIPTestCase {
	return []clientIPTestCase{
		// --- S1: source.ip is present and authoritative ----------------------
		{
			name:         "source.ip with no X-Forwarded-For",
			integration:  GCPServiceExtensionIntegration,
			attributes:   testSourceIPAttributes(structpb.NewStringValue("111.222.111.222")),
			wantClientIP: "111.222.111.222",
		},
		{
			name:           "source.ip beats a forged public X-Forwarded-For",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 18.18.18.18, 8.233.57.190"},
			wantClientIP:   "18.18.18.18",
		},
		{
			name:           "source.ip is trusted even when private",
			integration:    GCPServiceExtensionIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("192.168.0.9")),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77"},
			wantClientIP:   "192.168.0.9",
		},
		{
			name:         "host:port source.ip is accepted",
			integration:  GCPServiceExtensionIntegration,
			attributes:   testSourceIPAttributes(structpb.NewStringValue("203.0.113.77:57360")),
			wantClientIP: "203.0.113.77",
		},
		{
			name:         "bracketed IPv6 source.ip is unwrapped",
			integration:  GCPServiceExtensionIntegration,
			attributes:   testSourceIPAttributes(structpb.NewStringValue("[2001:db8::1]:443")),
			wantClientIP: "2001:db8::1",
		},
		{
			name:         "IPv4-mapped IPv6 source.ip is collapsed",
			integration:  GCPServiceExtensionIntegration,
			attributes:   testSourceIPAttributes(structpb.NewStringValue("::ffff:203.0.113.77")),
			wantClientIP: "203.0.113.77",
		},

		// --- S2: zero configuration, GCLB-shaped X-Forwarded-For -------------
		// Google Cloud appends two entries of its own, so the client observed by
		// the load balancer is always the second-to-last one:
		// https://cloud.google.com/load-balancing/docs/https#x-forwarded-for_header
		{
			name:           "GCLB-shaped X-Forwarded-For resolves to the observed client",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
			wantClientIP:   "82.67.164.163",
		},
		{
			name:           "GCLB-shaped X-Forwarded-For with no forged prefix",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "82.67.164.163, 8.233.57.190"},
			wantClientIP:   "82.67.164.163",
		},
		{
			name:           "internal load balancer keeps its private forwarding rule IP last",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 10.128.0.7"},
			wantClientIP:   "82.67.164.163",
		},
		{
			name:           "several forged entries do not shift the position",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "1.1.1.1, 2.2.2.2, 82.67.164.163, 8.233.57.190"},
			wantClientIP:   "82.67.164.163",
		},
		{
			name:           "IPv6 client observed by GCLB",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "2001:db8::1, 8.233.57.190"},
			wantClientIP:   "2001:db8::1",
		},

		// --- S3: not enough to decide, fall back to the default resolver -----
		{
			name:           "single X-Forwarded-For entry resolves nothing",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77"},
		},
		{
			name:        "absent X-Forwarded-For resolves nothing",
			integration: GCPServiceExtensionIntegration,
		},
		{
			name:           "empty X-Forwarded-For resolves nothing",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": ""},
		},
		{
			name:           "unparseable second-to-last entry resolves nothing",
			integration:    GCPServiceExtensionIntegration,
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, not-an-address, 8.233.57.190"},
		},
		{
			name:        "malformed source.ip falls through to the X-Forwarded-For rule",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceIPAttribute: structpb.NewStringValue("not-an-address"),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
			wantClientIP:   "82.67.164.163",
		},
		{
			name:        "non-string source.ip falls through to the X-Forwarded-For rule",
			integration: GCPServiceExtensionIntegration,
			attributes: testExtProcAttributes(map[string]*structpb.Value{
				testSourceIPAttribute: structpb.NewNumberValue(57360),
			}),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77"},
		},
		{
			name:           "attribute namespace absent resolves from the header only",
			integration:    GCPServiceExtensionIntegration,
			attributes:     map[string]*structpb.Struct{"some.other.filter": {}},
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77"},
		},
		{
			// mergeMetadataHeaders fills an absent X-Forwarded-For from the ext_proc
			// stream's metadata. That header describes the gRPC connection carrying
			// the callout, not the request that crossed the load balancer, so the
			// positional rule must not read it.
			name:        "X-Forwarded-For present only in stream metadata resolves nothing",
			integration: GCPServiceExtensionIntegration,
			metadata:    metadata.Pairs("x-forwarded-for", "203.0.113.77", "x-forwarded-for", "82.67.164.163"),
		},
		{
			// Same, but source.ip is authoritative regardless of which transport the
			// header came from.
			name:         "stream-metadata X-Forwarded-For does not stop source.ip",
			integration:  GCPServiceExtensionIntegration,
			metadata:     metadata.Pairs("x-forwarded-for", "203.0.113.77", "x-forwarded-for", "82.67.164.163"),
			attributes:   testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			wantClientIP: "18.18.18.18",
		},
		{
			name:        "zone-scoped source.ip is rejected",
			integration: GCPServiceExtensionIntegration,
			attributes:  testSourceIPAttributes(structpb.NewStringValue("fe80::1%eth0")),
		},

		// --- S4: anything but GCP Service Extensions is left alone -----------
		{
			name:           "plain envoy integration resolves nothing",
			integration:    EnvoyIntegration,
			attributes:     testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
		},
		{
			name:           "envoy declared through the integration header resolves nothing",
			integration:    GCPServiceExtensionIntegration,
			metadata:       metadata.Pairs(datadogEnvoyIntegrationHeader, "1"),
			attributes:     testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
		},
		{
			// An embedder that left Integration unset is defaulted to GCP, but the
			// GCLB positional rule must not be applied to it: stock Envoy appends a
			// single entry, so len-2 would be a client-supplied value.
			name:                  "undeclared integration does not get the positional rule",
			integration:           GCPServiceExtensionIntegration,
			undeclaredIntegration: true,
			requestHeaders:        map[string]string{"X-Forwarded-For": "82.67.164.163, 203.0.113.9, 10.0.0.5"},
		},
		{
			// source.ip is infrastructure-set and absent from stock Envoy, so it stays
			// trustworthy even when the integration was not named.
			name:                  "undeclared integration still trusts source.ip",
			integration:           GCPServiceExtensionIntegration,
			undeclaredIntegration: true,
			attributes:            testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders:        map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
			wantClientIP:          "18.18.18.18",
		},
		{
			name:           "istio declared through the integration header resolves nothing",
			integration:    GCPServiceExtensionIntegration,
			metadata:       metadata.Pairs(datadogIntegrationHeader, "1"),
			attributes:     testSourceIPAttributes(structpb.NewStringValue("18.18.18.18")),
			requestHeaders: map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
		},
	}
}

func (tc clientIPTestCase) extract(t *testing.T) (request extractedRequest) {
	t.Helper()

	headers := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: ":method", RawValue: []byte("GET")},
			{Key: ":path", RawValue: []byte("/")},
			{Key: ":scheme", RawValue: []byte("https")},
			{Key: ":authority", RawValue: []byte("datadoghq.com")},
		},
	}
	for k, v := range tc.requestHeaders {
		headers.Headers = append(headers.Headers, &corev3.HeaderValue{Key: k, RawValue: []byte(v)})
	}

	msg := messageRequestHeaders{
		ProcessingRequest:   &extproc.ProcessingRequest{Attributes: tc.attributes},
		HttpHeaders:         &extproc.HttpHeaders{Headers: headers},
		integration:         tc.integration,
		integrationDeclared: !tc.undeclaredIntegration,
	}

	ctx := context.Background()
	if tc.metadata != nil {
		ctx = metadata.NewIncomingContext(ctx, tc.metadata)
	}

	pseudoRequest, err := msg.ExtractRequest(ctx)
	require.NoError(t, err)
	return extractedRequest{pseudoRequest.ClientIP, pseudoRequest.RemoteIP, pseudoRequest.Headers["X-Forwarded-For"]}
}

type extractedRequest struct {
	clientIP     netip.Addr
	remoteIP     netip.Addr
	forwardedFor []string
}

// TestExtractRequestClientIP pins which address the GCP Service Extension
// integration declares as the client, covering scenarios S1 through S4.
func TestExtractRequestClientIP(t *testing.T) {
	for _, tc := range clientIPTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.extract(t)

			if tc.wantClientIP == "" {
				require.False(t, got.clientIP.IsValid(), "expected no resolved identity, got %s", got.clientIP)
				require.False(t, got.remoteIP.IsValid(), "expected no resolved identity, got %s", got.remoteIP)
				return
			}

			require.Equal(t, tc.wantClientIP, got.clientIP.String())
			require.Equal(t, tc.wantClientIP, got.remoteIP.String(),
				"the address the load balancer observed is both the peer and the client")
		})
	}
}

// TestExtractRequestPreservesXFFVerbatim is what actually protects the WAF's
// view of the request: whatever we conclude about identity, X-Forwarded-For must
// reach the WAF exactly as the load balancer sent it, ordering and untrusted
// prefix included. This is scenario S5.
func TestExtractRequestPreservesXFFVerbatim(t *testing.T) {
	for _, tc := range clientIPTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.extract(t)

			if raw, ok := tc.requestHeaders["X-Forwarded-For"]; ok {
				// The request carried the header: it must arrive untouched, and
				// stream metadata must not be allowed to displace it.
				require.Equal(t, []string{raw}, got.forwardedFor)
				return
			}
			// The request carried no header. mergeMetadataHeaders then substitutes
			// the ext_proc stream's copy, which is long-standing behaviour and is
			// what the WAF has always inspected in that case — identity resolution
			// is what must ignore it, not the header view.
			require.Equal(t, tc.metadata.Get("x-forwarded-for"), got.forwardedFor)
		})
	}
}

// TestExtractRequestTooFewHeaders covers a malformed ProcessingRequest carrying
// fewer entries than there are pseudo-headers, which had no coverage. It must be
// rejected as a bad request.
func TestExtractRequestTooFewHeaders(t *testing.T) {
	msg := messageRequestHeaders{
		ProcessingRequest: &extproc.ProcessingRequest{},
		HttpHeaders: &extproc.HttpHeaders{Headers: &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":method", RawValue: []byte("GET")}},
		}},
		integration:         GCPServiceExtensionIntegration,
		integrationDeclared: true,
	}

	_, err := msg.ExtractRequest(context.Background())
	require.Error(t, err, "a request missing pseudo-headers must be rejected, not panic")
}
