// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// peerTagKeys are the network.destination.* tags of client spans.
var peerTagKeys = []string{ext.NetworkDestinationIP, ext.NetworkDestinationName, ext.NetworkDestinationPort}

// startMockTracer starts a mock tracer that is stopped when the test ends.
func startMockTracer(t *testing.T) mocktracer.Tracer {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	return mt
}

// tagsWithPrefix returns the span's tags whose keys start with prefix.
func tagsWithPrefix(span *mocktracer.Span, prefix string) map[string]any {
	tags := make(map[string]any)
	for key, value := range span.Tags() {
		if strings.HasPrefix(key, prefix) {
			tags[key] = value
		}
	}
	return tags
}

func TestProtocolFor(t *testing.T) {
	connectTags := Protocol{
		system:              "connectrpc",
		statusCodeTag:       "rpc.connect_rpc.error_code",
		requestTag:          "connect.request",
		statusDetailsPrefix: "connect.status_details.",
		notModifiedEligible: true,
	}
	grpcTags := Protocol{
		system:              "grpc",
		grpcStatus:          true,
		statusCodeTag:       "rpc.grpc.status_code",
		requestTag:          "grpc.request",
		statusDetailsPrefix: "grpc.status_details.",
	}
	// An unknown protocol sets none of the tags that name a protocol.
	unknownTags := Protocol{requestTag: "connect.request", statusDetailsPrefix: "connect.status_details."}
	for _, test := range []struct {
		protocol string
		want     Protocol
	}{
		{protocol: "connect", want: connectTags},
		{protocol: "grpc", want: grpcTags},
		{protocol: "grpcweb", want: grpcTags},
		{protocol: "", want: unknownTags},
		{protocol: "future-protocol", want: unknownTags},
	} {
		t.Run(fmt.Sprintf("%q", test.protocol), func(t *testing.T) {
			got := ProtocolFor(test.protocol)
			assert.Equal(t, test.want, *got)
		})
	}

	// Without a wire protocol, as over connect.v2's connectinprocess, a not-modified error is not a
	// 304.
	t.Run("in process", func(t *testing.T) {
		want := connectTags
		want.notModifiedEligible = false
		assert.Equal(t, want, *InProcessProtocol())
	})
}

func TestProcedureTags(t *testing.T) {
	for _, test := range []struct {
		name string
		spec Spec
		want map[string]any
	}{
		{
			name: "unary", spec: Spec{Procedure: "/acme.v1.Svc/Get", StreamType: StreamTypeUnary},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Get", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Get", "connect.method.kind": "unary"},
		},
		{
			name: "client stream", spec: Spec{Procedure: "/acme.v1.Svc/Upload", StreamType: StreamTypeClient},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Upload", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Upload", "connect.method.kind": "client_streaming"},
		},
		{
			name: "server stream", spec: Spec{Procedure: "/acme.v1.Svc/Watch", StreamType: StreamTypeServer},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Watch", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Watch", "connect.method.kind": "server_streaming"},
		},
		{
			name: "bidi stream", spec: Spec{Procedure: "/acme.v1.Svc/Chat", StreamType: StreamTypeBidi},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Chat", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Chat", "connect.method.kind": "bidi_streaming"},
		},
		{
			name: "unknown stream type", spec: Spec{Procedure: "/acme.v1.Svc/Chat", StreamType: StreamType(4)},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Chat", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Chat", "connect.method.kind": "unknown"},
		},
		{
			name: "largest stream type", spec: Spec{Procedure: "/acme.v1.Svc/Chat", StreamType: StreamType(255)},
			want: map[string]any{ext.ResourceName: "/acme.v1.Svc/Chat", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "Chat", "connect.method.kind": "unknown"},
		},
		{
			name: "procedure without a method", spec: Spec{Procedure: "service"},
			want: map[string]any{ext.ResourceName: "service", ext.RPCService: "service", ext.RPCMethod: "", "connect.method.kind": "unary"},
		},
		{
			name: "unrecognized", spec: Spec{Procedure: "/acme.v1.Svc/Unknown", StreamType: StreamTypeBidi, Unrecognized: true},
			want: map[string]any{
				ext.ResourceName: "_OTHER", ext.RPCService: "acme.v1.Svc", ext.RPCMethod: "_OTHER", "rpc.method_original": "Unknown",
				"connect.method.kind": "bidi_streaming",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]any{}
			addProcedureTags(tags, test.spec)
			assert.Equal(t, test.want, tags)
		})
	}
}

func TestProtocolTags(t *testing.T) {
	const procedure = "/acme.v1.Svc/Get"
	for _, test := range []struct {
		name     string
		protocol *Protocol
		want     map[string]any
	}{
		{name: `"connect"`, protocol: ProtocolFor("connect"), want: map[string]any{ext.RPCSystem: "connectrpc"}},
		{name: `"grpc"`, protocol: ProtocolFor("grpc"), want: map[string]any{ext.RPCSystem: "grpc", ext.GRPCFullMethod: procedure}},
		{name: `"grpcweb"`, protocol: ProtocolFor("grpcweb"), want: map[string]any{ext.RPCSystem: "grpc", ext.GRPCFullMethod: procedure}},
		{name: `""`, protocol: ProtocolFor(""), want: map[string]any{}},
		{name: `"future-protocol"`, protocol: ProtocolFor("future-protocol"), want: map[string]any{}},
		{name: "in process", protocol: InProcessProtocol(), want: map[string]any{ext.RPCSystem: "connectrpc"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]any{}
			addProtocolTags(tags, test.protocol, procedure)
			assert.Equal(t, test.want, tags)

			mt := startMockTracer(t)
			span := tracer.StartSpan("protocol")
			SetProtocolTags(span, test.protocol, procedure)
			span.Finish()
			got := map[string]any{}
			for _, key := range []string{ext.RPCSystem, ext.GRPCFullMethod} {
				if value := mt.FinishedSpans()[0].Tag(key); value != nil {
					got[key] = value
				}
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func TestPeerTags(t *testing.T) {
	// The tracer stores the port as a float64 metric.
	for _, test := range []struct {
		addr string
		want map[string]any
	}{
		{addr: "127.0.0.1:8080", want: map[string]any{ext.NetworkDestinationIP: "127.0.0.1", ext.NetworkDestinationPort: 8080.0}},
		{addr: "[::1]:443", want: map[string]any{ext.NetworkDestinationIP: "::1", ext.NetworkDestinationPort: 443.0}},
		{addr: "::1", want: map[string]any{ext.NetworkDestinationIP: "::1"}},
		// A URL host has brackets but no port for http://[::1]/.
		{addr: "[::1]", want: map[string]any{ext.NetworkDestinationIP: "::1"}},
		// A link-local peer, such as http://[fe80::1%25lo0]:62086/, keeps its zone.
		{addr: "[fe80::1%lo0]:62086", want: map[string]any{ext.NetworkDestinationIP: "fe80::1%lo0", ext.NetworkDestinationPort: 62086.0}},
		{addr: "[fe80::1%eth0]", want: map[string]any{ext.NetworkDestinationIP: "fe80::1%eth0"}},
		{addr: "fe80::1%eth0", want: map[string]any{ext.NetworkDestinationIP: "fe80::1%eth0"}},
		// IPv6 addresses are tagged in their canonical form: lowercase, with zeros compressed.
		{addr: "[2001:DB8::68]:8080", want: map[string]any{ext.NetworkDestinationIP: "2001:db8::68", ext.NetworkDestinationPort: 8080.0}},
		{addr: "[0:0:0:0:0:0:0:1]:443", want: map[string]any{ext.NetworkDestinationIP: "::1", ext.NetworkDestinationPort: 443.0}},
		{addr: "[FE80::1%eth0]", want: map[string]any{ext.NetworkDestinationIP: "fe80::1%eth0"}},
		// IPv4-mapped peers are tagged as IPv4 addresses, which have no zone.
		{addr: "[::ffff:1.2.3.4]:80", want: map[string]any{ext.NetworkDestinationIP: "1.2.3.4", ext.NetworkDestinationPort: 80.0}},
		{addr: "::ffff:1.2.3.4", want: map[string]any{ext.NetworkDestinationIP: "1.2.3.4"}},
		{addr: "[::ffff:1.2.3.4%eth0]:80", want: map[string]any{ext.NetworkDestinationIP: "1.2.3.4", ext.NetworkDestinationPort: 80.0}},
		{addr: "example.com", want: map[string]any{ext.NetworkDestinationName: "example.com"}},
		{addr: "example.com:80", want: map[string]any{ext.NetworkDestinationName: "example.com", ext.NetworkDestinationPort: 80.0}},
		{addr: "example.com:http", want: map[string]any{ext.NetworkDestinationName: "example.com"}},
		{addr: "example.com:", want: map[string]any{ext.NetworkDestinationName: "example.com"}},
		{addr: "", want: map[string]any{}},
	} {
		t.Run(fmt.Sprintf("%q", test.addr), func(t *testing.T) {
			mt := startMockTracer(t)
			span := tracer.StartSpan("peer")
			NewPeerTags(test.addr).Set(span)
			span.Finish()
			got := map[string]any{}
			for _, key := range peerTagKeys {
				if value := mt.FinishedSpans()[0].Tag(key); value != nil {
					got[key] = value
				}
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func TestMetadataTags(t *testing.T) {
	header := map[string][]string{
		"X-Test":              {"a", "b"},
		"X-Secret":            {"hidden"},
		"X-Other":             {"other"},
		"Authorization":       {"Bearer secret"},
		"Cookie":              {"session=secret"},
		"Traceparent":         {"00-1-2-01"},
		"X-Datadog-Trace-Id":  {"1"},
		"B3":                  {"trace-span-1"},
		"Payload-Bin":         {"YmluYXJ5"},
		"Ot-Baggage-Secret":   {"secret"},
		"Proxy-Authorization": {"secret"},
	}
	for _, test := range []struct {
		name    string
		setters []func(*Config)
		want    map[string]any
	}{
		{name: "disabled by default", want: map[string]any{}},
		{
			name: "binary, baggage and sensitive keys are excluded", setters: []func(*Config){(*Config).EnableMetadataTags},
			want: map[string]any{
				"rpc.request.metadata.x-test.0":   "a",
				"rpc.request.metadata.x-test.1":   "b",
				"rpc.request.metadata.x-secret.0": "hidden",
				"rpc.request.metadata.x-other.0":  "other",
			},
		},
		{
			name:    "ignored keys are case-insensitive",
			setters: []func(*Config){(*Config).EnableMetadataTags, func(c *Config) { c.IgnoreMetadata("X-SECRET", "x-oThEr") }},
			want:    map[string]any{"rpc.request.metadata.x-test.0": "a", "rpc.request.metadata.x-test.1": "b"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			span := tracer.StartSpan("metadata")
			newTestConfig(test.setters...).SetMetadataTags(span, maps.All(header))
			span.Finish()
			assert.Equal(t, test.want, tagsWithPrefix(mt.FinishedSpans()[0], "rpc.request.metadata."))
		})
	}

	t.Run("ignored keys are per configuration", func(t *testing.T) {
		mt := startMockTracer(t)
		_ = newTestConfig(func(c *Config) { c.IgnoreMetadata("x-test") })
		span := tracer.StartSpan("metadata")
		newTestConfig((*Config).EnableMetadataTags).SetMetadataTags(span, maps.All(header))
		span.Finish()
		assert.Equal(t, "a", mt.FinishedSpans()[0].Tag("rpc.request.metadata.x-test.0"))
	})

	// Without a span, the metadata is not read.
	t.Run("nil span", func(t *testing.T) {
		read := false
		all := func(func(string, []string) bool) { read = true }
		newTestConfig((*Config).EnableMetadataTags).SetMetadataTags(nil, all)
		assert.False(t, read)
	})
}

func TestSetRequestTags(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol *Protocol
		request  any
		want     map[string]any // a nil value means the tag is absent
	}{
		{name: "connect", protocol: &connectProtocol, request: wrapperspb.String("hello"), want: map[string]any{"connect.request": `"hello"`}},
		{name: "without a protocol", protocol: &unknownProtocol, request: wrapperspb.String("hello"), want: map[string]any{"connect.request": `"hello"`}},
		{name: "in process", protocol: &inProcessProtocol, request: wrapperspb.String("hello"), want: map[string]any{"connect.request": `"hello"`}},
		{name: "gRPC", protocol: &grpcProtocol, request: wrapperspb.String("hello"), want: map[string]any{"grpc.request": `"hello"`, "connect.request": nil}},
		{name: "not a protobuf message", protocol: &connectProtocol, request: "hello", want: map[string]any{"connect.request": nil}},
		{name: "unencodable message", protocol: &connectProtocol, request: wrapperspb.String("\xff"), want: map[string]any{"connect.request": nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			span := tracer.StartSpan("request")
			setRequestTags(span, test.request, test.protocol)
			span.Finish()
			for key, value := range test.want {
				assert.Equal(t, value, mt.FinishedSpans()[0].Tag(key), key)
			}
		})
	}

	t.Run("nil span", func(t *testing.T) {
		assert.NotPanics(t, func() {
			setRequestTags(nil, wrapperspb.String("hello"), &connectProtocol)
		})
	})

	for _, test := range []struct {
		name    string
		setters []func(*Config)
		want    any
	}{
		{name: "disabled"},
		{name: "enabled", setters: []func(*Config){(*Config).EnableRequestTags}, want: `"hello"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			span := tracer.StartSpan("request")
			newTestConfig(test.setters...).SetRequestTags(span, wrapperspb.String("hello"), &connectProtocol)
			span.Finish()
			assert.Equal(t, test.want, mt.FinishedSpans()[0].Tag("connect.request"))
		})
	}
}
