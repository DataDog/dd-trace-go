// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
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
