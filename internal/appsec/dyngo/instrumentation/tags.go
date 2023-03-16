// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package instrumentation

import (
	"net"
	"net/textproto"
	"os"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

const (
	// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
	envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"
)

var (
	ipv6SpecialNetworks = []*NetaddrIPPrefix{
		ippref("fec0::/10"), // site local
	}
	// List of IP-related headers leveraged to retrieve the public client IP address.
	// The order matters and is the one in which ClientIPTags will look up the HTTP headers.
	DefaultIPHeaders = []string{
		"x-forwarded-for",
		"x-real-ip",
		"true-client-ip",
		"x-client-ip",
		"x-forwarded",
		"forwarded-for",
		"x-cluster-client-ip",
		"fastly-client-ip",
		"cf-connecting-ip",
		"cf-connecting-ip6",
	}
	clientIPHeaderCfg string
)

func init() {
	// Required by sort.SearchStrings
	clientIPHeaderCfg = os.Getenv(envClientIPHeader)
}

// ippref returns the IP network from an IP address string s. If not possible, it returns nil.
func ippref(s string) *NetaddrIPPrefix {
	if prefix, err := NetaddrParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// ClientIPTags returns the resulting Datadog span tags `http.client_ip`
// containing the client IP and `network.client.ip` containing the remote IP.
// The tags are present only if a valid ip address has been returned by
// ClientIP().
func ClientIPTags(hdrs map[string][]string, hasCanonicalMIMEHeaderKeys bool, remoteAddr string) (tags map[string]string, clientIP NetaddrIP) {
	remoteIP, clientIP := ClientIP(hdrs, hasCanonicalMIMEHeaderKeys, remoteAddr)

	remoteIPValid := remoteIP.IsValid()
	clientIPValid := clientIP.IsValid()
	if !remoteIPValid && !clientIPValid {
		return nil, NetaddrIP{}
	}

	tags = make(map[string]string, 2)
	if remoteIPValid {
		tags["network.client.ip"] = remoteIP.String()
	}
	if clientIPValid {
		tags[ext.HTTPClientIP] = clientIP.String()
	}

	return tags, clientIP
}

// ClientIP returns the first public IP address found in the given headers. If
// none is present, it returns the first valid IP address present, possibly
// being a local IP address. The remote address, when valid, is used as fallback
// when no IP address has been found at all.
func ClientIP(hdrs map[string][]string, hasCanonicalMIMEHeaderKeys bool, remoteAddr string) (remoteIP, clientIP NetaddrIP) {
	monitoredHeaders := DefaultIPHeaders
	if clientIPHeaderCfg != "" {
		monitoredHeaders = []string{clientIPHeaderCfg}
	}

	// Walk IP-related headers
	var foundIP NetaddrIP
	for _, headerName := range monitoredHeaders {
		if hasCanonicalMIMEHeaderKeys {
			headerName = textproto.CanonicalMIMEHeaderKey(headerName)
		}

		headerValues, exists := hdrs[headerName]
		if !exists {
			continue // this monitored header is not present
		}

		// Assuming a list of comma-separated IP addresses, split them and build
		// the list of values to try to parse as IP addresses
		var ips []string
		for _, ip := range headerValues {
			ips = append(ips, strings.Split(ip, ",")...)
		}

		// Look for the first valid or global IP address in the comma-separated list
		for _, ipstr := range ips {
			ip := parseIP(strings.TrimSpace(ipstr))
			if !ip.IsValid() {
				continue
			}
			// Replace foundIP if still not valid in order to keep the oldest
			if !foundIP.IsValid() {
				foundIP = ip
			}
			if isGlobal(ip) {
				foundIP = ip
				break
			}
		}
	}

	// Decide which IP address is the client one by starting with the remote IP
	if ip := parseIP(remoteAddr); ip.IsValid() {
		remoteIP = ip
		clientIP = ip
	}

	// The IP address found in the headers supersedes a private remote IP address.
	if foundIP.IsValid() && !isGlobal(remoteIP) || isGlobal(foundIP) {
		clientIP = foundIP
	}

	return remoteIP, clientIP
}

func parseIP(s string) NetaddrIP {
	if ip, err := NetaddrParseIP(s); err == nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		if ip, err := NetaddrParseIP(h); err == nil {
			return ip
		}
	}
	return NetaddrIP{}
}

func isGlobal(ip NetaddrIP) bool {
	// IsPrivate also checks for ipv6 ULA.
	// We care to check for these addresses are not considered public, hence not global.
	// See https://www.rfc-editor.org/rfc/rfc4193.txt for more details.
	isGlobal := ip.IsValid() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	if !isGlobal || !ip.Is6() {
		return isGlobal
	}
	for _, n := range ipv6SpecialNetworks {
		if n.Contains(ip) {
			return false
		}
	}
	return isGlobal
}
