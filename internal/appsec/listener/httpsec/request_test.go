// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"context"
	_ "embed" // For go:embed
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/DataDog/appsec-internal-go/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/go-libddwaf/v4"
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
							"appsec.event":    true,
							"_dd.origin":      "appsec",
							"_dd.p.ts":        internal.TraceSourceTagValue{Value: internal.ASMTraceSource},
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

//go:embed testdata/trace_tagging_rules.json
var wafRulesJSON []byte

func TestTraceTagging(t *testing.T) {
	if usable, err := libddwaf.Usable(); !usable {
		t.Skipf("libddwaf is not usable in this context: %v", err)
	}

	wafManager, err := config.NewWAFManager(appsec.ObfuscatorConfig{}, wafRulesJSON)
	require.NoError(t, err)
	cfg := config.Config{
		WAFManager:          wafManager,
		WAFTimeout:          time.Hour,
		TraceRateLimit:      1_000,
		APISec:              appsec.APISecConfig{Enabled: true, Sampler: apisec.NewSampler(0)},
		RC:                  nil,
		RASP:                false,
		SupportedAddresses:  config.NewAddressSet([]string{"server.request.headers.no_cookies"}),
		MetaStructAvailable: true,
		BlockingUnavailable: false,
		TracingAsTransport:  false,
	}

	rootOp := dyngo.NewRootOperation()
	feat, err := waf.NewWAFFeature(&cfg, rootOp)
	require.NoError(t, err)
	defer feat.Stop()

	feat, err = NewHTTPSecFeature(&cfg, rootOp)
	require.NoError(t, err)
	defer feat.Stop()

	type testCase struct {
		UserAgent    string
		ExpectedTags map[string]any
	}
	testCases := map[string]testCase{
		"Attributes, No Keep, No Event": {
			UserAgent: "TraceTagging/v1+test",
			ExpectedTags: map[string]any{
				"_dd.appsec.trace.integer": int64(662607015),
				"_dd.appsec.trace.agent":   "TraceTagging/v1+test",
			},
		},
		"Attributes, Keep, No Event": {
			UserAgent: "TraceTagging/v2+test",
			ExpectedTags: map[string]any{
				ext.ManualKeep:             true,
				"_dd.appsec.trace.integer": int64(602214076),
				"_dd.appsec.trace.agent":   "TraceTagging/v2+test",
			},
		},
		"Attributes, Keep, Event": {
			UserAgent: "TraceTagging/v3+test",
			ExpectedTags: map[string]any{
				ext.ManualKeep:             true,
				"appsec.event":             true,
				"_dd.appsec.trace.integer": int64(299792458),
				"_dd.appsec.trace.agent":   "TraceTagging/v3+test",
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ctx = dyngo.RegisterOperation(ctx, rootOp)

			var span MockSpan
			op, _, _ := httpsec.StartOperation(ctx, httpsec.HandlerOperationArgs{
				Framework:    "test/phony",
				Method:       "GET",
				RequestURI:   "/fake/test/uri",
				RequestRoute: "/fake/:id/uri",
				Host:         "localhost",
				RemoteAddr:   "127.0.0.1:4242",
				Headers:      map[string][]string{"user-agent": {tc.UserAgent}},
				Cookies:      map[string][]string{},
				QueryParams:  map[string][]string{},
				PathParams:   map[string]string{"id": "test"},
			}, &span)
			op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})

			require.Subset(t, span.Tags, tc.ExpectedTags)
		})
	}
}
