// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"io"
	"maps"
	"testing"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

const (
	gcpServiceExtensionAttributeNamespace = "envoy.filters.http.ext_proc"
	forgedXForwardedFor                   = "198.51.100.42"
)

func TestGCPServiceExtensionSourceIPAuthoritative(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	t.Cleanup(httptrace.ResetCfg)
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	t.Run("valid", func(t *testing.T) {
		tests := []struct {
			name      string
			sourceIP  string
			canonical string
		}{
			{name: "public IPv4", sourceIP: "203.0.113.10", canonical: "203.0.113.10"},
			{name: "private IPv4", sourceIP: "10.20.30.40", canonical: "10.20.30.40"},
			{name: "IPv6", sourceIP: "2001:0db8:0000:0000:0000:0000:0000:0001", canonical: "2001:db8::1"},
			{name: "IPv4-mapped IPv6", sourceIP: "::ffff:203.0.113.11", canonical: "203.0.113.11"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, tags := runSourceIPRequest(t, GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue(tc.sourceIP)))

				require.Equal(t, tc.canonical, tags[ext.NetworkClientIP])
				require.Equal(t, tc.canonical, tags[ext.HTTPClientIP])
				require.Equal(t, forgedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
			})
		}
	})

	t.Run("present invalid suppresses XFF", func(t *testing.T) {
		tests := []struct {
			name  string
			value *structpb.Value
		}{
			{name: "nil", value: nil},
			{name: "empty", value: structpb.NewStringValue("")},
			{name: "malformed", value: structpb.NewStringValue("not-an-ip")},
			{name: "scoped IPv6", value: structpb.NewStringValue("fe80::1%eth0")},
			{name: "number", value: structpb.NewNumberValue(42)},
			{name: "boolean", value: structpb.NewBoolValue(true)},
			{name: "null", value: structpb.NewNullValue()},
			{name: "struct", value: structpb.NewStructValue(&structpb.Struct{})},
			{name: "list", value: structpb.NewListValue(&structpb.ListValue{})},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, tags := runSourceIPRequest(t, GCPServiceExtensionIntegration, nil, sourceIPAttributes(tc.value))

				require.NotContains(t, tags, ext.NetworkClientIP)
				require.NotContains(t, tags, ext.HTTPClientIP)
				require.Equal(t, forgedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
			})
		}
	})

	t.Run("absent preserves legacy resolution", func(t *testing.T) {
		tests := []struct {
			name       string
			attributes map[string]*structpb.Struct
		}{
			{name: "missing namespace"},
			{name: "missing field", attributes: map[string]*structpb.Struct{
				gcpServiceExtensionAttributeNamespace: {},
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, tags := runSourceIPRequest(t, GCPServiceExtensionIntegration, nil, tc.attributes)

				require.NotContains(t, tags, ext.NetworkClientIP)
				require.Equal(t, forgedXForwardedFor, tags[ext.HTTPClientIP])
				require.Equal(t, forgedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
			})
		}
	})

	t.Run("ignored outside effective GCP", func(t *testing.T) {
		tests := []struct {
			name        string
			integration Integration
			metadata    metadata.MD
		}{
			{name: "Envoy integration", integration: EnvoyIntegration},
			{name: "Envoy Gateway integration", integration: EnvoyGatewayIntegration},
			{name: "Istio integration", integration: IstioIntegration},
			{name: "Envoy metadata override", integration: GCPServiceExtensionIntegration, metadata: metadata.Pairs(datadogEnvoyIntegrationHeader, "1")},
			{name: "Istio metadata override", integration: GCPServiceExtensionIntegration, metadata: metadata.Pairs(datadogIntegrationHeader, "1")},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, tags := runSourceIPRequest(t, tc.integration, tc.metadata, sourceIPAttributes(structpb.NewStringValue("203.0.113.12")))

				require.NotContains(t, tags, ext.NetworkClientIP)
				require.Equal(t, forgedXForwardedFor, tags[ext.HTTPClientIP])
				require.Equal(t, forgedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
			})
		}
	})
}

func TestMessageRequestHeadersIgnoresSourceIPForNonGCP(t *testing.T) {
	tests := []struct {
		name        string
		integration Integration
	}{
		{name: "Envoy", integration: EnvoyIntegration},
		{name: "Envoy Gateway", integration: EnvoyGatewayIntegration},
		{name: "Istio", integration: IstioIntegration},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			processingRequest := &envoyextproc.ProcessingRequest{
				Attributes: sourceIPAttributes(structpb.NewStringValue("203.0.113.14")),
			}
			headers := &envoyextproc.HttpHeaders{
				Headers: makeRequestHeaders(t, nil, "GET", "/"),
			}

			message := messageRequestHeaders{
				ProcessingRequest: processingRequest,
				HttpHeaders:       headers,
				integration:       tc.integration,
			}
			ip, set := message.ClientIPOverride(context.Background())

			require.False(t, set)
			require.False(t, ip.IsValid())
		})
	}
}

func TestGCPServiceExtensionSourceIPIsWAFIdentity(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	t.Cleanup(httptrace.ResetCfg)
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	response, tags := runSourceIPRequest(t, GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("111.222.111.222")))

	require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, response.GetResponse())
	require.Equal(t, "111.222.111.222", tags[ext.NetworkClientIP])
	require.Equal(t, "111.222.111.222", tags[ext.HTTPClientIP])
	require.Equal(t, forgedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
	require.Equal(t, "true", tags["appsec.blocked"])
}

func TestGCPServiceExtensionSourceIPSuppressesBlockedXFF(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	t.Cleanup(httptrace.ResetCfg)
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	const blockedXForwardedFor = "111.222.111.222"
	tests := []struct {
		name       string
		value      *structpb.Value
		expectedIP string
	}{
		{name: "valid safe source", value: structpb.NewStringValue("203.0.113.15"), expectedIP: "203.0.113.15"},
		{name: "present invalid source", value: structpb.NewStringValue("not-an-ip")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response, tags := runSourceIPRequestWithXFF(t, GCPServiceExtensionIntegration, nil, sourceIPAttributes(tc.value), blockedXForwardedFor)

			require.IsType(t, &envoyextproc.ProcessingResponse_RequestHeaders{}, response.GetResponse())
			require.Nil(t, response.GetImmediateResponse())
			require.NotContains(t, tags, "appsec.blocked")
			require.Equal(t, blockedXForwardedFor, tags["http.request.headers.x-forwarded-for"])
			if tc.expectedIP == "" {
				require.NotContains(t, tags, ext.NetworkClientIP)
				require.NotContains(t, tags, ext.HTTPClientIP)
				return
			}
			require.Equal(t, tc.expectedIP, tags[ext.NetworkClientIP])
			require.Equal(t, tc.expectedIP, tags[ext.HTTPClientIP])
		})
	}
}

func TestGCPServiceExtensionSourceIPCollectionActivation(t *testing.T) {
	t.Cleanup(httptrace.ResetCfg)

	tests := []struct {
		name              string
		clientIPEnabled   string
		expectedSourceTag bool
	}{
		{name: "client IP collection enabled", clientIPEnabled: "true", expectedSourceTag: true},
		{name: "AppSec and client IP collection disabled", clientIPEnabled: "false", expectedSourceTag: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_APPSEC_ENABLED", "false")
			t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", tc.clientIPEnabled)
			httptrace.ResetCfg()

			_, tags := runSourceIPRequest(t, GCPServiceExtensionIntegration, nil, sourceIPAttributes(structpb.NewStringValue("203.0.113.13")))

			if tc.expectedSourceTag {
				require.Equal(t, "203.0.113.13", tags[ext.NetworkClientIP])
				require.Equal(t, "203.0.113.13", tags[ext.HTTPClientIP])
			} else {
				require.NotContains(t, tags, ext.NetworkClientIP)
				require.NotContains(t, tags, ext.HTTPClientIP)
			}
			require.NotContains(t, tags, "http.request.headers.x-forwarded-for")
		})
	}
}

func TestGCPServiceExtensionSourceIPAbsentUsesMetadataConnectionAddress(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	t.Cleanup(httptrace.ResetCfg)
	httptrace.ResetCfg()

	const metadataAddress = "192.0.2.20"
	_, tags := runSourceIPRequestWithXFF(
		t,
		GCPServiceExtensionIntegration,
		metadata.Pairs("x-forwarded-for", metadataAddress),
		nil,
		"",
	)

	require.Equal(t, metadataAddress, tags[ext.NetworkClientIP])
	require.Equal(t, metadataAddress, tags[ext.HTTPClientIP])
	require.NotContains(t, tags, "http.request.headers.x-forwarded-for")
}

func sourceIPAttributes(value *structpb.Value) map[string]*structpb.Struct {
	return map[string]*structpb.Struct{
		gcpServiceExtensionAttributeNamespace: {
			Fields: map[string]*structpb.Value{"source.ip": value},
		},
	}
}

func runSourceIPRequest(t *testing.T, integration Integration, md metadata.MD, attributes map[string]*structpb.Struct) (*envoyextproc.ProcessingResponse, map[string]any) {
	t.Helper()
	return runSourceIPRequestWithXFF(t, integration, md, attributes, forgedXForwardedFor)
}

func runSourceIPRequestWithXFF(t *testing.T, integration Integration, md metadata.MD, attributes map[string]*structpb.Struct, xForwardedFor string) (*envoyextproc.ProcessingResponse, map[string]any) {
	t.Helper()
	var headers map[string]string
	if xForwardedFor != "" {
		headers = map[string]string{"X-Forwarded-For": xForwardedFor}
	}

	rig, err := newEnvoyAppsecRig(t, integration, false, nil)
	require.NoError(t, err)
	defer rig.Close()

	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	stream, err := rig.client.Process(ctx)
	require.NoError(t, err)

	err = stream.Send(&envoyextproc.ProcessingRequest{
		Attributes: attributes,
		Request: &envoyextproc.ProcessingRequest_RequestHeaders{
			RequestHeaders: &envoyextproc.HttpHeaders{
				Headers:     makeRequestHeaders(t, headers, "GET", "/"),
				EndOfStream: true,
			},
		},
	})
	require.NoError(t, err)

	response, err := stream.Recv()
	require.NoError(t, err)
	if response.GetImmediateResponse() == nil {
		require.NotNil(t, response.GetRequestHeaders())
		sendProcessingResponseHeaders(t, stream, nil, "200", false)
		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF)
	}

	require.NoError(t, stream.CloseSend())
	_, _ = stream.Recv()

	finished := mt.FinishedSpans()
	require.Len(t, finished, 1)
	tags := make(map[string]any, len(finished[0].Tags()))
	maps.Copy(tags, finished[0].Tags())
	return response, tags
}
