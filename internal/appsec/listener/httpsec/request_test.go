// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name             string
		addr             net.Addr
		md               metadata.MD
		expectedClientIP string
	}{
		{
			name:             "tcp-ipv4-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			expectedClientIP: "1.2.3.4",
		},
		{
			name:             "tcp-ipv4-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			md:               map[string][]string{"x-client-ip": {"127.0.0.1, 2.3.4.5"}},
			expectedClientIP: "2.3.4.5",
		},
		{
			name:             "tcp-ipv6-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("::1"), Port: 6789},
			expectedClientIP: "::1",
		},
		{
			name:             "udp-ipv4-address",
			addr:             &net.UDPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			expectedClientIP: "1.2.3.4",
		},
		{
			name:             "udp-ipv6-address",
			addr:             &net.UDPAddr{IP: net.ParseIP("::1"), Port: 6789},
			expectedClientIP: "::1",
		},
		{
			name: "unix-socket-address",
			addr: &net.UnixAddr{Name: "/var/my.sock"},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, clientIP := ClientIPTags(tc.md, false, tc.addr.String())
			expectedClientIP, _ := netip.ParseAddr(tc.expectedClientIP)
			require.Equal(t, expectedClientIP.String(), clientIP.String())
		})
	}
}

func TestNormalizeHTTPHeaders(t *testing.T) {
	for _, tc := range []struct {
		headers  map[string][]string
		expected map[string]string
	}{
		{
			headers:  nil,
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie": {"not-collected"},
			},
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8",
			},
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8", "9.10.11.12,13.14.15.16"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8,9.10.11.12,13.14.15.16",
			},
		},
	} {
		headers := NormalizeHTTPHeaders(tc.headers)
		require.Equal(t, tc.expected, headers)
	}
}

type MockSpan struct {
	Tags map[string]any
}

func (m *MockSpan) SetTag(key string, value interface{}) {
	if m.Tags == nil {
		m.Tags = make(map[string]any)
	}
	if key == ext.ManualKeep {
		if value == samplernames.AppSec {
			m.Tags[ext.ManualKeep] = true
		}
	} else {
		m.Tags[key] = value
	}
}

func TestTags(t *testing.T) {
	for _, eventCase := range []struct {
		name          string
		events        []any
		expectedTag   string
		expectedError bool
	}{
		{
			name:   "no-event",
			events: nil,
		},
		{
			name:        "one-event",
			events:      []any{"one"},
			expectedTag: `{"triggers":["one"]}`,
		},
		{
			name:        "two-events",
			events:      []any{"one", "two"},
			expectedTag: `{"triggers":["one","two"]}`,
		},
	} {
		eventCase := eventCase
		for _, reqHeadersCase := range []struct {
			name         string
			headers      map[string][]string
			expectedTags map[string]interface{}
		}{
			{
				name: "zero-headers",
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4", "4.5.6.7"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]interface{}{
					"http.request.headers.x-forwarded-for": "1.2.3.4,4.5.6.7",
				},
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]interface{}{
					"http.request.headers.x-forwarded-for": "1.2.3.4",
				},
			},
			{
				name: "no-monitored-headers",
				headers: map[string][]string{
					"my-header": {"something"},
				},
			},
		} {
			reqHeadersCase := reqHeadersCase
			for _, respHeadersCase := range []struct {
				name         string
				headers      map[string][]string
				expectedTags map[string]interface{}
			}{
				{
					name: "zero-headers",
				},
				{
					name: "ct-header",
					headers: map[string][]string{
						"Content-Type": {"application/json"},
						"my-header":    {"something"},
					},
					expectedTags: map[string]interface{}{
						"http.response.headers.content-type": "application/json",
					},
				},
				{
					name: "no-monitored-headers",
					headers: map[string][]string{
						"my-header": {"something"},
					},
				},
			} {
				respHeadersCase := respHeadersCase
				t.Run(fmt.Sprintf("%s-%s-%s", eventCase.name, reqHeadersCase.name, respHeadersCase.name), func(t *testing.T) {
					var span MockSpan
					waf.SetEventSpanTags(&span)
					value, err := json.Marshal(map[string][]any{"triggers": eventCase.events})
					if eventCase.expectedError {
						require.Error(t, err)
						return
					}

					span.SetTag("_dd.appsec.json", string(value))

					require.NoError(t, err)
					setRequestHeadersTags(&span, reqHeadersCase.headers)
					setResponseHeadersTags(&span, respHeadersCase.headers)

					if eventCase.events != nil {
						require.Subset(t, span.Tags, map[string]interface{}{
							"_dd.appsec.json": eventCase.expectedTag,
							"manual.keep":     true,
							"appsec.event":    true,
							"_dd.origin":      "appsec",
							"_dd.p.appsec":    internal.PropagatingTagValue{Value: "1"},
						})
					}

					if l := len(reqHeadersCase.expectedTags); l > 0 {
						require.Subset(t, span.Tags, reqHeadersCase.expectedTags)
					}

					if l := len(respHeadersCase.expectedTags); l > 0 {
						require.Subset(t, span.Tags, respHeadersCase.expectedTags)
					}
				})
			}
		}
	}
}
