// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"inet.af/netaddr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestStartRequestSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
	s, _ := StartRequestSpan(r)
	s.Finish()
	spans := mt.FinishedSpans()

	require.Len(t, spans, 1)
	assert.Equal(t, "example.com", spans[0].Tag("http.host"))
}

type IPTestCase struct {
	name       string
	remoteAddr string
	headers    map[string]string
	expectedIP netaddr.IP
}

func genIPTestCases() []IPTestCase {
	ipv4Global := randGlobalIPv4().String()
	ipv6Global := randGlobalIPv6().String()
	ipv4Private := randPrivateIPv4().String()
	ipv6Private := randPrivateIPv6().String()
	tcs := []IPTestCase{}
	// Simple ipv4 test cases over all headers
	for _, header := range ipHeaders {
		tcs = append(tcs, IPTestCase{
			name:       "ipv4-global." + header,
			headers:    map[string]string{header: ipv4Global},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		})
		tcs = append(tcs, IPTestCase{
			name:       "ipv4-private." + header,
			headers:    map[string]string{header: ipv4Private},
			expectedIP: netaddr.IP{},
		})
	}
	// Simple ipv6 test cases over all headers
	for _, header := range ipHeaders {
		tcs = append(tcs, IPTestCase{
			name:       "ipv6-global." + header,
			headers:    map[string]string{header: ipv6Global},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		})
		tcs = append(tcs, IPTestCase{
			name:       "ipv6-private." + header,
			headers:    map[string]string{header: ipv6Private},
			expectedIP: netaddr.IP{},
		})
	}
	// private and global in same header
	tcs = append([]IPTestCase{
		{
			name:       "ipv4-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv4Global},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv4Global + "," + ipv4Private},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		},
		{
			name:       "ipv6-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv6Private + "," + ipv6Global},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv6Global + "," + ipv6Private},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		},
	}, tcs...)
	// Invalid IPs (or a mix of valid/invalid over a single or multiple headers)
	tcs = append([]IPTestCase{
		{
			name:       "invalid-ipv4",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1"},
			expectedIP: netaddr.IP{},
		},
		{
			name:       "invalid-ipv4-recover",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1, " + ipv4Global},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		},
		{
			name:       "invalid-ipv4-recover-multi-header-1",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1", "forwarded-for": ipv4Global},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		},
		{
			name:       "invalid-ipv4-recover-multi-header-2",
			headers:    map[string]string{"forwarded-for": ipv4Global, "x-forwarded-for": "127..0.0.1"},
			expectedIP: netaddr.MustParseIP(ipv4Global),
		},
		{
			name:       "invalid-ipv6",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::"},
			expectedIP: netaddr.IP{},
		},
		{
			name:       "invalid-ipv6-recover",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::, " + ipv6Global},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		},
		{
			name:       "invalid-ipv6-recover-multi-header-1",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::", "forwarded-for": ipv6Global},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		},
		{
			name:       "invalid-ipv6-recover-multi-header-2",
			headers:    map[string]string{"forwarded-for": ipv6Global, "x-forwarded-for": "2001:0db8:2001:zzzz::"},
			expectedIP: netaddr.MustParseIP(ipv6Global),
		},
	}, tcs...)
	tcs = append([]IPTestCase{
		{
			name:       "no-headers",
			expectedIP: netaddr.IP{},
		},
		{
			name:       "header-case",
			expectedIP: netaddr.MustParseIP(ipv4Global),
			headers:    map[string]string{"X-fOrWaRdEd-FoR": ipv4Global},
		},
	}, tcs...)

	return tcs
}

func TestIPHeaders(t *testing.T) {
	for _, tc := range genIPTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			for k, v := range tc.headers {
				header.Add(k, v)
			}
			require.Equal(t, tc.expectedIP.String(), getClientIP(tc.remoteAddr, header).String())
		})
	}

}

func randIPv4() netaddr.IP {
	return netaddr.IPv4(uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()))
}

func randIPv6() netaddr.IP {
	return netaddr.IPv6Raw([16]byte{
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
	})
}

func randGlobalIPv4() netaddr.IP {
	for {
		ip := randIPv4()
		if isGlobal(ip) {
			return ip
		}
	}
}

func randGlobalIPv6() netaddr.IP {
	for {
		ip := randIPv6()
		if isGlobal(ip) {
			return ip
		}
	}
}

func randPrivateIPv4() netaddr.IP {
	for {
		ip := randIPv4()
		if !isGlobal(ip) && ip.IsPrivate() {
			return ip
		}
	}
}

func randPrivateIPv6() netaddr.IP {
	for {
		ip := randIPv6()
		if !isGlobal(ip) && ip.IsPrivate() {
			return ip
		}
	}
}
