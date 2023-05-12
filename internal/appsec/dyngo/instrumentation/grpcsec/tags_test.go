// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"

	"github.com/DataDog/appsec-internal-go/netip"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestSetSecurityEventTags(t *testing.T) {
	for _, eventCase := range []struct {
		name          string
		events        []json.RawMessage
		expectedTag   string
		expectedError bool
	}{
		{
			name:        "one-event",
			events:      []json.RawMessage{json.RawMessage(`["one","two"]`)},
			expectedTag: `{"triggers":["one","two"]}`,
		},
		{
			name:          "one-event-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one",two"]`)},
			expectedError: true,
		},
		{
			name:        "two-events",
			events:      []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three","four"]`)},
			expectedTag: `{"triggers":["one","two","three","four"]}`,
		},
		{
			name:          "two-events-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three,"four"]`)},
			expectedError: true,
		},
		{
			name:          "three-events-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three","four"]`), json.RawMessage(`"five"`)},
			expectedError: true,
		},
	} {
		eventCase := eventCase
		for _, metadataCase := range []struct {
			name         string
			md           map[string][]string
			expectedTags map[string]string
		}{
			{
				name: "zero-metadata",
			},
			{
				name: "xff-metadata",
				md: map[string][]string{
					"x-forwarded-for": {"1.2.3.4", "4.5.6.7"},
					":authority":      {"something"},
				},
				expectedTags: map[string]string{
					"grpc.metadata.x-forwarded-for": "1.2.3.4,4.5.6.7",
				},
			},
			{
				name: "xff-metadata",
				md: map[string][]string{
					"x-forwarded-for": {"1.2.3.4"},
					":authority":      {"something"},
				},
				expectedTags: map[string]string{
					"grpc.metadata.x-forwarded-for": "1.2.3.4",
				},
			},
			{
				name: "no-monitored-metadata",
				md: map[string][]string{
					":authority": {"something"},
				},
			},
		} {
			metadataCase := metadataCase
			t.Run(fmt.Sprintf("%s-%s", eventCase.name, metadataCase.name), func(t *testing.T) {
				var span MockSpan
				err := setSecurityEventTags(&span, eventCase.events, metadataCase.md)
				if eventCase.expectedError {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)

				expectedTags := map[string]interface{}{
					"_dd.appsec.json": eventCase.expectedTag,
					"manual.keep":     true,
					"appsec.event":    true,
					"_dd.origin":      "appsec",
				}

				for k, v := range metadataCase.expectedTags {
					expectedTags[k] = v
				}

				require.Equal(t, expectedTags, span.tags)
				require.False(t, span.finished)
			})
		}
	}
}

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
			_, clientIP := httpsec.ClientIPTags(tc.md, false, tc.addr.String())
			expectedClientIP, _ := netip.ParseAddr(tc.expectedClientIP)
			require.Equal(t, expectedClientIP.String(), clientIP.String())
		})
	}
}

type MockSpan struct {
	tags     map[string]interface{}
	finished bool
}

func (m *MockSpan) SetTag(key string, value interface{}) {
	if m.tags == nil {
		m.tags = make(map[string]interface{})
	}
	if key == ext.ManualKeep {
		if value == samplernames.AppSec {
			m.tags[ext.ManualKeep] = true
		}
	} else {
		m.tags[key] = value
	}
}

func (m *MockSpan) SetOperationName(_ string) {
	panic("unused")
}

func (m *MockSpan) BaggageItem(_ string) string {
	panic("unused")
}

func (m *MockSpan) SetBaggageItem(_, _ string) {
	panic("unused")
}

func (m *MockSpan) Finish(_ ...ddtrace.FinishOption) {
	m.finished = true
}

func (m *MockSpan) Context() ddtrace.SpanContext {
	panic("unused")
}
