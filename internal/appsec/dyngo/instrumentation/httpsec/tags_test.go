// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"

	"github.com/stretchr/testify/require"
)

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

type ipTestCase struct {
	name           string
	remoteAddr     string
	headers        map[string]string
	expectedIP     instrumentation.NetaddrIP
	clientIPHeader string
}

func genIPTestCases() []ipTestCase {
	ipv4Global := randGlobalIPv4().String()
	ipv6Global := randGlobalIPv6().String()
	ipv4Private := randPrivateIPv4().String()
	ipv6Private := randPrivateIPv6().String()

	tcs := []ipTestCase{
		{
			name:       "ipv4-global-remoteaddr",
			remoteAddr: ipv4Global,
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-private-remoteaddr",
			remoteAddr: ipv4Private,
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Private),
		},
		{
			name:       "ipv6-global-remoteaddr",
			remoteAddr: ipv6Global,
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-private-remoteaddr",
			remoteAddr: ipv6Private,
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Private),
		},
	}

	// Simple ipv4 test cases over all headers
	for _, header := range defaultIPHeaders {
		tcs = append(tcs,
			ipTestCase{
				name:       "ipv4-global." + header,
				remoteAddr: ipv4Private,
				headers:    map[string]string{header: ipv4Global},
				expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
			},
			ipTestCase{
				name:       "ipv4-private." + header,
				headers:    map[string]string{header: ipv4Private},
				remoteAddr: ipv6Private,
				expectedIP: instrumentation.NetaddrMustParseIP(ipv4Private),
			},
			ipTestCase{
				name:       "ipv4-global-remoteaddr-local-ip-header." + header,
				remoteAddr: ipv4Global,
				headers:    map[string]string{header: ipv4Private},
				expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
			},
			ipTestCase{
				name:       "ipv4-global-remoteaddr-global-ip-header." + header,
				remoteAddr: ipv6Global,
				headers:    map[string]string{header: ipv4Global},
				expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
			})
	}

	// Simple ipv6 test cases over all headers
	for _, header := range defaultIPHeaders {
		tcs = append(tcs, ipTestCase{
			name:       "ipv6-global." + header,
			remoteAddr: ipv4Private,
			headers:    map[string]string{header: ipv6Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
			ipTestCase{
				name:       "ipv6-private." + header,
				headers:    map[string]string{header: ipv6Private},
				remoteAddr: ipv4Private,
				expectedIP: instrumentation.NetaddrMustParseIP(ipv6Private),
			},
			ipTestCase{
				name:       "ipv6-global-remoteaddr-local-ip-header." + header,
				remoteAddr: ipv6Global,
				headers:    map[string]string{header: ipv6Private},
				expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
			},
			ipTestCase{
				name:       "ipv6-global-remoteaddr-global-ip-header." + header,
				remoteAddr: ipv4Global,
				headers:    map[string]string{header: ipv6Global},
				expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
			})
	}

	// private and global in same header
	tcs = append([]ipTestCase{
		{
			name:       "ipv4-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv4Global + "," + ipv4Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv6-private+global",
			headers:    map[string]string{"x-forwarded-for": ipv6Private + "," + ipv6Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-global+private",
			headers:    map[string]string{"x-forwarded-for": ipv6Global + "," + ipv6Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "mixed-global+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv6Global + "," + ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "mixed-global+global",
			headers:    map[string]string{"x-forwarded-for": ipv4Private + "," + ipv4Global + "," + ipv6Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
	}, tcs...)

	// Invalid IPs (or a mix of valid/invalid over a single or multiple headers)
	tcs = append([]ipTestCase{
		{
			name:       "no headers",
			headers:    nil,
			expectedIP: instrumentation.NetaddrIP{},
		},
		{
			name:       "invalid-ipv4",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1"},
			expectedIP: instrumentation.NetaddrIP{},
		},
		{
			name:       "invalid-ipv4-header-valid-remoteaddr",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1"},
			remoteAddr: ipv4Private,
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Private),
		},
		{
			name:       "invalid-ipv4-recover",
			headers:    map[string]string{"x-forwarded-for": "127..0.0.1, " + ipv6Private + "," + ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ip-multi-header-order-0",
			headers:    map[string]string{"forwarded-for": ipv6Global, "cf-connecting-ip": ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ip-multi-header-order-1",
			headers:    map[string]string{"forwarded-for": ipv6Global, "x-client-ip": ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-multi-header-0",
			headers:    map[string]string{"x-forwarded-for": ipv4Private, "forwarded-for": ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-multi-header-1",
			headers:    map[string]string{"x-forwarded-for": ipv4Global, "forwarded-for": ipv4Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-multi-header-2",
			headers:    map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv4Private, "forwarded-for": fmt.Sprintf("%s, %s", ipv4Private, ipv4Global)},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "ipv4-multi-header-3",
			headers:    map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv4Global, "forwarded-for": ipv4Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:       "invalid-ipv6",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::"},
			expectedIP: instrumentation.NetaddrIP{},
		},
		{
			name:       "invalid-ipv6-recover",
			headers:    map[string]string{"x-forwarded-for": "2001:0db8:2001:zzzz::, " + ipv6Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-multi-header-0",
			headers:    map[string]string{"x-forwarded-for": ipv6Private, "forwarded-for": ipv6Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-multi-header-1",
			headers:    map[string]string{"x-forwarded-for": ipv6Global, "forwarded-for": ipv6Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-multi-header-2",
			headers:    map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv6Private, "forwarded-for": fmt.Sprintf("%s, %s", ipv6Private, ipv6Global)},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "ipv6-multi-header-3",
			headers:    map[string]string{"x-forwarded-for": "127.0.0.1, " + ipv6Global, "forwarded-for": ipv6Private},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv6Global),
		},
		{
			name:       "no-headers",
			expectedIP: instrumentation.NetaddrIP{},
		},
		{
			name:       "header-case",
			headers:    map[string]string{"X-fOrWaRdEd-FoR": ipv4Global},
			expectedIP: instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:           "user-header",
			headers:        map[string]string{"x-forwarded-for": ipv6Global, "custom-header": ipv4Global},
			clientIPHeader: "custom-header",
			expectedIP:     instrumentation.NetaddrMustParseIP(ipv4Global),
		},
		{
			name:           "user-header-not-found",
			headers:        map[string]string{"x-forwarded-for": ipv4Global},
			clientIPHeader: "custom-header",
			expectedIP:     instrumentation.NetaddrIP{},
		},
	}, tcs...)

	return tcs
}

func TestClientIP(t *testing.T) {
	for _, hasCanonicalMIMEHeaderKeys := range []bool{true, false} {
		t.Run(fmt.Sprintf("canonical-headers-%t", hasCanonicalMIMEHeaderKeys), func(t *testing.T) {
			// Make sure to restore the real value of clientIPHeaderCfg at the end of the test
			defer func(s string) { clientIPHeaderCfg = s }(clientIPHeaderCfg)
			for _, tc := range genIPTestCases() {
				t.Run(tc.name, func(t *testing.T) {
					header := http.Header{}
					for k, v := range tc.headers {
						if hasCanonicalMIMEHeaderKeys {
							header.Add(k, v)
						} else {
							k = strings.ToLower(k)
							header[k] = append(header[k], v)
						}
					}
					clientIPHeaderCfg = tc.clientIPHeader
					tags, clientIP := ClientIPTags(header, hasCanonicalMIMEHeaderKeys, tc.remoteAddr)
					if tc.expectedIP.IsValid() {
						expectedIP := tc.expectedIP.String()
						require.Equal(t, expectedIP, tags[ext.HTTPClientIP])
						require.Equal(t, expectedIP, clientIP.String())
					} else {
						require.NotContains(t, tags, ext.HTTPClientIP)
					}
				})
			}
		})
	}
}

func randIPv4() instrumentation.NetaddrIP {
	return instrumentation.NetaddrIPv4(uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()))
}

func randIPv6() instrumentation.NetaddrIP {
	return instrumentation.NetaddrIPv6Raw([16]byte{
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
		uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()), uint8(rand.Uint32()),
	})
}

func randGlobalIPv4() instrumentation.NetaddrIP {
	for {
		ip := randIPv4()
		if isGlobal(ip) {
			return ip
		}
	}
}

func randGlobalIPv6() instrumentation.NetaddrIP {
	for {
		ip := randIPv6()
		if isGlobal(ip) {
			return ip
		}
	}
}

func randPrivateIPv4() instrumentation.NetaddrIP {
	for {
		ip := randIPv4()
		if !isGlobal(ip) && ip.IsPrivate() {
			return ip
		}
	}
}

func randPrivateIPv6() instrumentation.NetaddrIP {
	for {
		ip := randIPv6()
		if !isGlobal(ip) && ip.IsPrivate() {
			return ip
		}
	}
}
