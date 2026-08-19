// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHTTPAuthority(t *testing.T) {
	for _, tt := range []struct {
		name      string
		authority string
		want      parsedHTTPAuthority
	}{
		{
			name:      "hostname without port",
			authority: "example.com",
			want:      parsedHTTPAuthority{address: "example.com", port: -1, portState: authorityPortAbsent, valid: true},
		},
		{
			name:      "IPv4 with port",
			authority: "192.0.2.1:8080",
			want:      parsedHTTPAuthority{address: "192.0.2.1", port: 8080, portState: authorityPortValid, valid: true},
		},
		{
			name:      "bracketed IPv6 without port",
			authority: "[2001:db8::1]",
			want:      parsedHTTPAuthority{address: "2001:db8::1", port: -1, portState: authorityPortAbsent, valid: true},
		},
		{
			name:      "scoped IPv6 with port",
			authority: "[fe80::1%eth0]:8080",
			want:      parsedHTTPAuthority{address: "fe80::1%eth0", port: 8080, portState: authorityPortValid, valid: true},
		},
		{
			name:      "zero port",
			authority: "example.com:0",
			want:      parsedHTTPAuthority{address: "example.com", port: 0, portState: authorityPortValid, valid: true},
		},
		{
			name:      "empty port",
			authority: "example.com:",
			want:      parsedHTTPAuthority{address: "example.com", port: -1, portState: authorityPortInvalid, valid: true},
		},
		{
			name:      "non-numeric port",
			authority: "example.com:http",
			want:      parsedHTTPAuthority{address: "example.com", port: -1, portState: authorityPortInvalid, valid: true},
		},
		{
			name:      "out-of-range port",
			authority: "example.com:65536",
			want:      parsedHTTPAuthority{address: "example.com", port: -1, portState: authorityPortInvalid, valid: true},
		},
		{name: "unbracketed IPv6", authority: "2001:db8::1"},
		{name: "missing IPv6 bracket", authority: "[2001:db8::1"},
		{name: "content after IPv6 bracket", authority: "[2001:db8::1]invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseHTTPAuthority(tt.authority))
		})
	}
}

func TestServerAddressPortFromClientRequest(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		host        string
		wantAddress string
		wantPort    int
	}{
		{name: "URL authority", target: "http://example.com:8080/path", wantAddress: "example.com", wantPort: 8080},
		{name: "implicit HTTP port", target: "http://example.com/path", wantAddress: "example.com", wantPort: 80},
		{name: "implicit HTTPS port", target: "https://example.com/path", wantAddress: "example.com", wantPort: 443},
		{name: "implicit non-HTTP port", target: "ftp://example.com/path", wantAddress: "example.com", wantPort: -1},
		{name: "IPv6", target: "http://[fe80::1%25eth0]:8080/path", wantAddress: "fe80::1%eth0", wantPort: 8080},
		{name: "IPv6 implicit port", target: "http://[2000::1]/path", wantAddress: "2000::1", wantPort: 80},
		{name: "Host authority", target: "http://1.2.3.4:8080/path", host: "example.com:9090", wantAddress: "example.com", wantPort: 9090},
		{name: "zero port", target: "http://example.com:0/path", wantAddress: "example.com", wantPort: 0},
		{name: "malformed Host authority", target: "http://example.com/path", host: "bad::authority", wantAddress: "bad::authority", wantPort: -1},
		{name: "invalid Host authority port", target: "http://example.com/path", host: "example.com:invalid", wantAddress: "example.com", wantPort: -1},
		{name: "out-of-range port", target: "http://example.com:65536/path", wantAddress: "example.com", wantPort: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := url.Parse(tt.target)
			require.NoError(t, err)
			req := &http.Request{URL: target, Host: tt.host}
			address, port := ServerAddressPortFromClientRequest(req)
			assert.Equal(t, tt.wantAddress, address)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}
