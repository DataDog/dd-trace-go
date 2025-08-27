// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/stretchr/testify/require"
)

type ipTestCase struct {
	name            string
	remoteAddr      string
	headers         map[string]string
	expectedIP      netip.Addr
	clientIPHeaders []string
}

func genIPTestCases() []ipTestCase {
	ipv4Global := randGlobalIPv4().String()
	ipv6Global := randGlobalIPv6().String()
	ipv4Private := randPrivateIPv4().String()
	k8sPrivate := randK8sPrivate().String()
	ipv6Private := randPrivateIPv6().String()

	tcs := []ipTestCase{
		{
			name:       "ipv4-global-remoteaddr",
			remoteAddr: ipv4Global,
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:       "ipv4-private-remoteaddr",
			remoteAddr: ipv4Private,
			expectedIP: netip.MustParseAddr(ipv4Private),
		},
		{
			name:       "ipv6-global-remoteaddr",
			remoteAddr: ipv6Global,
			expectedIP: netip.MustParseAddr(ipv6Global),
		},
		{
			name:       "ipv6-private-remoteaddr",
			remoteAddr: ipv6Private,
			expectedIP: netip.MustParseAddr(ipv6Private),
		},
	}

	// Simple ipv4 test cases over all headers
	for _, header := range defaultIPHeaders {
		if header == "forwarded" {
			// The `Forwarded` header has a different format to others, so it's handled separately.
			continue
		}

		tcs = append(tcs,
			ipTestCase{
				name:            "ipv4-global." + header,
				remoteAddr:      ipv4Private,
				headers:         map[string]string{header: ipv4Global},
				expectedIP:      netip.MustParseAddr(ipv4Global),
				clientIPHeaders: defaultIPHeaders,
			},
			ipTestCase{
				name:            "ipv4-private." + header,
				headers:         map[string]string{header: ipv4Private},
				remoteAddr:      ipv6Private,
				expectedIP:      netip.MustParseAddr(ipv4Private),
				clientIPHeaders: defaultIPHeaders,
			},
			ipTestCase{
				name:            "ipv4-global-remoteaddr-local-ip-header." + header,
				remoteAddr:      ipv4Global,
				headers:         map[string]string{header: ipv4Private},
				expectedIP:      netip.MustParseAddr(ipv4Global),
				clientIPHeaders: defaultIPHeaders,
			},
			ipTestCase{
				name:            "ipv4-global-remoteaddr-global-ip-header." + header,
				remoteAddr:      ipv6Global,
				headers:         map[string]string{header: ipv4Global},
				expectedIP:      netip.MustParseAddr(ipv4Global),
				clientIPHeaders: defaultIPHeaders,
			})
	}

	// Simple ipv6 test cases over all headers
	for _, header := range defaultIPHeaders {
		if header == "forwarded" {
			// The `Forwarded` header has a different format to others, so it's handled separately.
			continue
		}

		tcs = append(tcs, ipTestCase{
			name:            "ipv6-global." + header,
			remoteAddr:      ipv4Private,
			headers:         map[string]string{header: ipv6Global},
			expectedIP:      netip.MustParseAddr(ipv6Global),
			clientIPHeaders: defaultIPHeaders,
		},
			ipTestCase{
				name:            "ipv6-private." + header,
				headers:         map[string]string{header: ipv6Private},
				remoteAddr:      ipv4Private,
				expectedIP:      netip.MustParseAddr(ipv6Private),
				clientIPHeaders: defaultIPHeaders,
			},
			ipTestCase{
				name:            "ipv6-global-remoteaddr-local-ip-header." + header,
				remoteAddr:      ipv6Global,
				headers:         map[string]string{header: ipv6Private},
				expectedIP:      netip.MustParseAddr(ipv6Global),
				clientIPHeaders: defaultIPHeaders,
			},
			ipTestCase{
				name:            "ipv6-global-remoteaddr-global-ip-header." + header,
				remoteAddr:      ipv4Global,
				headers:         map[string]string{header: ipv6Global},
				expectedIP:      netip.MustParseAddr(ipv6Global),
				clientIPHeaders: defaultIPHeaders,
			})
	}

	// private and global in same header
	tcs = append([]ipTestCase{
		{
			name:       "ipv4-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv4Global},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:       "ipv4-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv4Global + "," + ipv4Private},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:       "ipv6-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv6Private + "," + ipv6Global},
			expectedIP: netip.MustParseAddr(ipv6Global),
		},
		{
			name:       "ipv6-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv6Global + "," + ipv6Private},
			expectedIP: netip.MustParseAddr(ipv6Global),
		},
		{
			name:       "mixed-global+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv6Global + "," + ipv4Global},
			expectedIP: netip.MustParseAddr(ipv6Global),
		},
		{
			name:       "mixed-global+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv4Global + "," + ipv6Global},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:       "k8s-private+global",
			headers:    map[string]string{"x-forwarded-for": k8sPrivate + "," + ipv4Global},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
	}, tcs...)

	// Invalid IPs (or a mix of valid/invalid over a single or multiple headers)
	tcs = append([]ipTestCase{
		{
			name:       "no headers",
			headers:    nil,
			expectedIP: netip.Addr{},
		},
		{
			name:       "invalid-ipv4",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1"},
			expectedIP: netip.Addr{},
		},
		{
			name:       "invalid-ipv4-header-valid-remoteaddr",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1"},
			remoteAddr: ipv4Private,
			expectedIP: netip.MustParseAddr(ipv4Private),
		},
		{
			name:       "invalid-ipv4-recover",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1, " + ipv6Private + "," + ipv4Global},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "ip-multi-header-order-0",
			headers:         map[string]string{"x-forwarded-for": ipv4Global, "forwarded-for": ipv6Global},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "ip-multi-header-order-1",
			headers:         map[string]string{"x-forwarded-for": ipv4Global, "forwarded-for": ipv6Global},
			clientIPHeaders: []string{"forwarded-for", "x-forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv6Global),
		},
		{
			name:            "ipv4-multi-header-0",
			headers:         map[string]string{"x-forwarded-for": ipv4Private, "forwarded-for": ipv4Global},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "ipv4-multi-header-1",
			headers:         map[string]string{"x-forwarded-for": ipv4Global, "forwarded-for": ipv4Private},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "ipv4-multi-header-2",
			headers:         map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv4Private, "forwarded-for": fmt.Sprintf("%s, %s", ipv4Private, ipv4Global)},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "ipv4-multi-header-3",
			headers:         map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv4Global, "forwarded-for": ipv4Private},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:       "invalid-ipv6",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::"},
			expectedIP: netip.Addr{},
		},
		{
			name:       "invalid-ipv6-recover",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::, " + ipv6Global},
			expectedIP: netip.MustParseAddr(ipv6Global),
		},
		{
			name:            "ipv6-multi-header-0",
			headers:         map[string]string{"x-forwarded-for": ipv6Private, "forwarded-for": ipv6Global},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv6Global),
		},
		{
			name:            "ipv6-multi-header-1",
			headers:         map[string]string{"x-forwarded-for": ipv6Global, "forwarded-for": ipv6Private},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv6Global),
		},
		{
			name:            "ipv6-multi-header-2",
			headers:         map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv6Private, "forwarded-for": fmt.Sprintf("%s, %s", ipv6Private, ipv6Global)},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv6Global),
		},
		{
			name:            "ipv6-multi-header-3",
			headers:         map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv6Global, "forwarded-for": ipv6Private},
			clientIPHeaders: []string{"x-forwarded-for", "forwarded-for"},
			expectedIP:      netip.MustParseAddr(ipv6Global),
		},
		{
			name:       "no-headers",
			expectedIP: netip.Addr{},
		},
		{
			name:       "header-case",
			headers:    map[string]string{"X-fOrWaRdEd-FoR": ipv4Global},
			expectedIP: netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "user-header",
			headers:         map[string]string{"x-forwarded-for": ipv6Global, "custom-header": ipv4Global},
			clientIPHeaders: []string{"custom-header"},
			expectedIP:      netip.MustParseAddr(ipv4Global),
		},
		{
			name:            "user-header-not-found",
			headers:         map[string]string{"x-forwarded-for": ipv4Global},
			clientIPHeaders: []string{"custom-header"},
			expectedIP:      netip.Addr{},
		},
	}, tcs...)

	// Special case for the Forwarded header (as it has its own format)
	tcs = append(tcs,
		// IPv4 flavor
		ipTestCase{
			name:            "ipv4-global.forwarded",
			remoteAddr:      ipv4Private,
			headers:         map[string]string{"forwarded": "for=" + ipv4Global},
			expectedIP:      netip.MustParseAddr(ipv4Global),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv4-private.forwarded",
			headers:         map[string]string{"forwarded": "for=" + ipv4Private},
			remoteAddr:      ipv6Private,
			expectedIP:      netip.MustParseAddr(ipv4Private),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv4-global-remoteaddr-local-ip-header.forwarded",
			remoteAddr:      ipv4Global,
			headers:         map[string]string{"forwarded": "for=" + ipv4Private},
			expectedIP:      netip.MustParseAddr(ipv4Global),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv4-global-remoteaddr-global-ip-header.forwarded",
			remoteAddr:      ipv6Global,
			headers:         map[string]string{"forwarded": "for=" + ipv4Global},
			expectedIP:      netip.MustParseAddr(ipv4Global),
			clientIPHeaders: defaultIPHeaders,
		},
		// IPv6 flavor
		ipTestCase{
			name:            "ipv6-global.forwarded",
			remoteAddr:      ipv4Private,
			headers:         map[string]string{"forwarded": `for="[` + ipv6Global + `]"`},
			expectedIP:      netip.MustParseAddr(ipv6Global),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv6-private.forwarded",
			headers:         map[string]string{"forwarded": `for="[` + ipv6Private + `]"`},
			remoteAddr:      ipv4Private,
			expectedIP:      netip.MustParseAddr(ipv6Private),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv6-global-remoteaddr-local-ip-header.forwarded",
			remoteAddr:      ipv6Global,
			headers:         map[string]string{"forwarded": `for="[` + ipv6Private + `]"`},
			expectedIP:      netip.MustParseAddr(ipv6Global),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "ipv6-global-remoteaddr-global-ip-header.forwarded",
			remoteAddr:      ipv4Global,
			headers:         map[string]string{"forwarded": `for="[` + ipv6Global + `]"`},
			expectedIP:      netip.MustParseAddr(ipv6Global),
			clientIPHeaders: defaultIPHeaders,
		},
		// Seen in the System-Tests
		ipTestCase{
			name:            "system-tests-ipv4.forwarded",
			remoteAddr:      ipv4Global,
			headers:         map[string]string{"forwarded": `for=127.0.0.1;host="example.host";by=2.2.2.2;proto=http,for="1.1.1.1:6543"`},
			expectedIP:      netip.MustParseAddr("1.1.1.1"),
			clientIPHeaders: defaultIPHeaders,
		},
		ipTestCase{
			name:            "system-tests-ipv6.forwarded",
			remoteAddr:      ipv6Global,
			headers:         map[string]string{"forwarded": `for="[::1]",for="[9f7b:5e67:5472:4464:90b0:6b0a:9aa6:f9dc]:4485"`},
			expectedIP:      netip.MustParseAddr("9f7b:5e67:5472:4464:90b0:6b0a:9aa6:f9dc"),
			clientIPHeaders: defaultIPHeaders,
		},
	)

	return tcs
}

func TestClientIPExtraction(t *testing.T) {
	for _, hasCanonicalMIMEHeaderKeys := range []bool{true, false} {
		t.Run(fmt.Sprintf("canonical-headers-%t", hasCanonicalMIMEHeaderKeys), func(t *testing.T) {
			for _, tc := range genIPTestCases() {
				t.Run(tc.name, func(t *testing.T) {
					headers := http.Header{}
					for k, v := range tc.headers {
						if hasCanonicalMIMEHeaderKeys {
							headers.Add(k, v)
						} else {
							k = strings.ToLower(k)
							headers[k] = append(headers[k], v)
						}
					}

					// Default list to use - the tests rely on x-forwarded-for only when using this default list
					monitoredHeaders := []string{"x-client-ip", "x-forwarded-for", "true-client-ip"}
					if tc.clientIPHeaders != nil {
						monitoredHeaders = tc.clientIPHeaders
					}
					remoteIP, clientIP := ClientIP(headers, hasCanonicalMIMEHeaderKeys, tc.remoteAddr, monitoredHeaders)
					tags := ClientIPTagsFor(remoteIP, clientIP)
					if tc.expectedIP.IsValid() {
						expectedIP := tc.expectedIP.String()
						require.Equal(t, expectedIP, clientIP.String())
						if tc.remoteAddr != "" {
							require.Equal(t, tc.remoteAddr, remoteIP.String())
							require.Equal(t, tc.remoteAddr, tags[ext.NetworkClientIP])
						} else {
							require.NotContains(t, tags, ext.NetworkClientIP)
						}
						require.Equal(t, expectedIP, tags[ext.HTTPClientIP])
					} else {
						require.NotContains(t, tags, ext.HTTPClientIP)
						require.False(t, clientIP.IsValid())
					}
				})
			}
		})
	}
}

func TestParseForwardedHeader(t *testing.T) {
	require.Equal(t,
		[]string{"127.0.0.1", "127.0.0.2", "fe80::2897:fcb4:830e:9e44", "quoted\"escaped", "fe80::2897:fcb4:830e:9e45"},
		parseForwardedHeader(`by=unknown;FOR="127.0.0.1:443";proto="https;TLS",for=127.0.0.2,for="[fe80::2897:fcb4:830e:9e44]:443",for="quoted\"escaped",for="[fe80::2897:fcb4:830e:9e45]"`),
	)
	require.Equal(t,
		[]string{"127.0.0.1"},
		parseForwardedHeader(`by=unknown,FOR="127.0.0.1:443"`),
	)
	require.Equal(t,
		[]string{"127.0.0.1"},
		parseForwardedHeader(`for="127.0.0.1";`),
	)
	require.Equal(t,
		[]string{},
		// Valid, but there is no `for` directive in there...
		parseForwardedHeader(`by=127.0.0.1;proto=https`),
	)
	require.Equal(t,
		[]string(nil),
		// Invalid: the quote is not properly closed
		parseForwardedHeader(`for="127.0.0.1`),
	)
}

func randIPv4() netip.Addr {
	return netip.AddrFrom4([4]byte{byte(rand.Uint32()), byte(rand.Uint32()), byte(rand.Uint32()), byte(rand.Uint32())})
}

func randIPv6() netip.Addr {
	return netip.AddrFrom16([16]byte{
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
	})
}

func randGlobalIPv4() netip.Addr {
	for {
		ip := randIPv4()
		if isGlobalIP(ip) {
			return ip
		}
	}
}

func randGlobalIPv6() netip.Addr {
	for {
		ip := randIPv6()
		if isGlobalIP(ip) {
			return ip
		}
	}
}

func randPrivateIPv4() netip.Addr {
	for {
		ip := randIPv4()
		if !isGlobalIP(ip) && ip.IsPrivate() {
			return ip
		}
	}
}

func randK8sPrivate() netip.Addr {
	for {
		// IPs in 100.65.0.0/10 (100.65.0.0 - 100.127.255.255) are considered private
		ip := netip.AddrFrom4([4]byte{100, 65 + byte(rand.Uint32()%64), byte(rand.Uint32()), byte(rand.Uint32())})
		if k8sInternalIPv4Prefix.Contains(ip) {
			return ip
		}
	}
}

func randPrivateIPv6() netip.Addr {
	for {
		ip := randIPv6()
		if !isGlobalIP(ip) && ip.IsPrivate() {
			return ip
		}
	}
}
