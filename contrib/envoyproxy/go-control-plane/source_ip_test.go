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

const forgedXForwardedFor = "198.51.100.42"

func TestGCPServiceExtensionSourceIPWAFIdentity(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	t.Cleanup(httptrace.ResetCfg)
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	tests := []struct {
		name    string
		value   *structpb.Value
		xff     string
		wantIP  string
		blocked bool
	}{
		{"source IP blocks", structpb.NewStringValue("111.222.111.222"), forgedXForwardedFor, "111.222.111.222", true},
		{"valid source suppresses blocked XFF", structpb.NewStringValue("203.0.113.15"), "111.222.111.222", "203.0.113.15", false},
		{"invalid source suppresses blocked XFF", structpb.NewStringValue("not-an-ip"), "111.222.111.222", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response, tags := runSourceIPRequestWith(t, sourceIPRequestOptions{
				attributes: sourceIPAttributes(tc.value), xForwardedFor: tc.xff,
			})
			if tc.blocked {
				require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, response.GetResponse())
				require.Equal(t, "true", tags["appsec.blocked"])
			} else {
				require.IsType(t, &envoyextproc.ProcessingResponse_RequestHeaders{}, response.GetResponse())
				require.NotContains(t, tags, "appsec.blocked")
			}
			requireSourceIPTags(t, tags, tc.wantIP)
			require.Equal(t, tc.xff, tags["http.request.headers.x-forwarded-for"])
		})
	}
}

func TestGCPServiceExtensionSourceIPCollection(t *testing.T) {
	t.Cleanup(httptrace.ResetCfg)
	metadataAddress := "192.0.2.20"
	tests := []struct {
		name       string
		enabled    string
		attributes map[string]*structpb.Struct
		metadata   metadata.MD
		xff        string
		want       string
	}{
		{"valid source without XFF", "true", sourceIPAttributes(structpb.NewStringValue("203.0.113.13")), nil, "", "203.0.113.13"},
		{"invalid source with forged XFF", "true", sourceIPAttributes(structpb.NewStringValue("not-an-ip")), nil, forgedXForwardedFor, ""},
		{"collection disabled", "false", sourceIPAttributes(structpb.NewStringValue("203.0.113.13")), nil, "", ""},
		{"absent source uses metadata", "true", nil, metadata.Pairs("x-forwarded-for", metadataAddress), "", metadataAddress},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_APPSEC_ENABLED", "false")
			t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", tc.enabled)
			httptrace.ResetCfg()
			_, tags := runSourceIPRequestWith(t, sourceIPRequestOptions{
				metadata: tc.metadata, attributes: tc.attributes, xForwardedFor: tc.xff,
			})
			requireSourceIPTags(t, tags, tc.want)
			require.NotContains(t, tags, "http.request.headers.x-forwarded-for")
		})
	}
}

type sourceIPRequestOptions struct {
	metadata      metadata.MD
	attributes    map[string]*structpb.Struct
	xForwardedFor string
}

func sourceIPAttributes(value *structpb.Value) map[string]*structpb.Struct {
	return map[string]*structpb.Struct{gcpServiceExtensionAttributesNamespace: {Fields: map[string]*structpb.Value{gcpServiceExtensionSourceIPAttribute: value}}}
}

func requireSourceIPTags(t *testing.T, tags map[string]any, want string) {
	t.Helper()
	if want == "" {
		require.NotContains(t, tags, ext.NetworkClientIP)
		require.NotContains(t, tags, ext.HTTPClientIP)
		return
	}
	require.Equal(t, want, tags[ext.NetworkClientIP])
	require.Equal(t, want, tags[ext.HTTPClientIP])
}

func runSourceIPRequestWith(t *testing.T, options sourceIPRequestOptions) (*envoyextproc.ProcessingResponse, map[string]any) {
	t.Helper()
	headers := map[string]string{}
	if options.xForwardedFor != "" {
		headers["X-Forwarded-For"] = options.xForwardedFor
	}
	rig, err := newEnvoyAppsecRig(t, GCPServiceExtensionIntegration, false, nil)
	require.NoError(t, err)
	defer rig.Close()
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx := metadata.NewOutgoingContext(context.Background(), options.metadata)
	stream, err := rig.client.Process(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&envoyextproc.ProcessingRequest{Attributes: options.attributes, Request: &envoyextproc.ProcessingRequest_RequestHeaders{
		RequestHeaders: &envoyextproc.HttpHeaders{Headers: makeRequestHeaders(t, headers, "GET", "/"), EndOfStream: true},
	}}))
	response, err := stream.Recv()
	require.NoError(t, err)
	if response.GetImmediateResponse() == nil {
		sendProcessingResponseHeaders(t, stream, nil, "200", false)
		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF)
	}
	require.NoError(t, stream.CloseSend())
	_, _ = stream.Recv()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return response, maps.Clone(spans[0].Tags())
}
