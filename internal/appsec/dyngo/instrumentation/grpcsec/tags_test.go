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

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
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
		for _, addrCase := range []struct {
			name        string
			addr        net.Addr
			expectedTag string
		}{
			{
				name:        "tcp-ipv4-address",
				addr:        &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
				expectedTag: "1.2.3.4",
			},
			{
				name:        "tcp-ipv6-address",
				addr:        &net.TCPAddr{IP: net.ParseIP("::1"), Port: 6789},
				expectedTag: "::1",
			},
			{
				name:        "udp-ipv4-address",
				addr:        &net.UDPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
				expectedTag: "1.2.3.4",
			},
			{
				name:        "udp-ipv6-address",
				addr:        &net.UDPAddr{IP: net.ParseIP("::1"), Port: 6789},
				expectedTag: "::1",
			},
			{
				name: "unix-socket-address",
				addr: &net.UnixAddr{Name: "/var/my.sock"},
			},
		} {
			addrCase := addrCase
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
				t.Run(fmt.Sprintf("%s-%s-%s", eventCase.name, addrCase.name, metadataCase.name), func(t *testing.T) {
					var span MockSpan
					err := setSecurityEventTags(&span, eventCase.events, addrCase.addr, metadataCase.md)
					if eventCase.expectedError {
						require.Error(t, err)
						return
					} else {
						require.NoError(t, err)
					}

					expectedTags := map[string]interface{}{
						"_dd.appsec.json": eventCase.expectedTag,
						"manual.keep":     true,
						"appsec.event":    true,
						"_dd.origin":      "appsec",
					}

					if addr := addrCase.expectedTag; addr != "" {
						expectedTags["network.client.ip"] = addr
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
}

type MockSpan struct {
	tags     map[string]interface{}
	finished bool
}

func (m *MockSpan) SetTag(key string, value interface{}) {
	if m.tags == nil {
		m.tags = make(map[string]interface{})
	}
	m.tags[key] = value
}

func (m *MockSpan) SetOperationName(operationName string) {
	panic("unused")
}

func (m *MockSpan) BaggageItem(key string) string {
	panic("unused")
}

func (m *MockSpan) SetBaggageItem(key, val string) {
	panic("unused")
}

func (m *MockSpan) Finish(opts ...ddtrace.FinishOption) {
	m.finished = true
}

func (m *MockSpan) Context() ddtrace.SpanContext {
	panic("unused")
}
