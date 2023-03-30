// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"encoding/json"
	"net"
	"net/textproto"
	"os"
	"sort"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
	envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"
)

var (
	ipv6SpecialNetworks = []*instrumentation.NetaddrIPPrefix{
		ippref("fec0::/10"), // site local
	}

	// List of IP-related headers leveraged to retrieve the public client IP address.
	// The order matters and is the one in which ClientIPTags will look up the HTTP headers.
	defaultIPHeaders = []string{
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

	// List of HTTP headers we collect and send.
	collectedHTTPHeaders = append(defaultIPHeaders,
		"host",
		"content-length",
		"content-type",
		"content-encoding",
		"content-language",
		"forwarded",
		"via",
		"user-agent",
		"accept",
		"accept-encoding",
		"accept-language")

	clientIPHeaderCfg string
)

func init() {
	// Required by sort.SearchStrings
	sort.Strings(collectedHTTPHeaders[:])
	clientIPHeaderCfg = os.Getenv(envClientIPHeader)
}

// SetSecurityEventTags sets the AppSec-specific span tags when a security event occurred into the service entry span.
func SetSecurityEventTags(span instrumentation.TagSetter, events []json.RawMessage, headers, respHeaders map[string][]string) {
	if err := instrumentation.SetEventSpanTags(span, events); err != nil {
		log.Error("appsec: unexpected error while creating the appsec event tags: %v", err)
	}
	for h, v := range NormalizeHTTPHeaders(headers) {
		span.SetTag("http.request.headers."+h, v)
	}
	for h, v := range NormalizeHTTPHeaders(respHeaders) {
		span.SetTag("http.response.headers."+h, v)
	}
}

// NormalizeHTTPHeaders returns the HTTP headers following Datadog's
// normalization format.
func NormalizeHTTPHeaders(headers map[string][]string) (normalized map[string]string) {
	if len(headers) == 0 {
		return nil
	}
	normalized = make(map[string]string)
	for k, v := range headers {
		k = strings.ToLower(k)
		if i := sort.SearchStrings(collectedHTTPHeaders[:], k); i < len(collectedHTTPHeaders) && collectedHTTPHeaders[i] == k {
			normalized[k] = strings.Join(v, ",")
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// ippref returns the IP network from an IP address string s. If not possible, it returns nil.
func ippref(s string) *instrumentation.NetaddrIPPrefix {
	if prefix, err := instrumentation.NetaddrParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// ClientIPTags returns the resulting Datadog span tags `http.client_ip`
// containing the client IP and `network.client.ip` containing the remote IP.
// The tags are present only if a valid ip address has been returned by
// ClientIP().
func ClientIPTags(hdrs map[string][]string, hasCanonicalMIMEHeaderKeys bool, remoteAddr string) (tags map[string]string, clientIP instrumentation.NetaddrIP) {
	remoteIP, clientIP := ClientIP(hdrs, hasCanonicalMIMEHeaderKeys, remoteAddr)

	remoteIPValid := remoteIP.IsValid()
	clientIPValid := clientIP.IsValid()
	if !remoteIPValid && !clientIPValid {
		return nil, instrumentation.NetaddrIP{}
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
func ClientIP(hdrs map[string][]string, hasCanonicalMIMEHeaderKeys bool, remoteAddr string) (remoteIP, clientIP instrumentation.NetaddrIP) {
	monitoredHeaders := defaultIPHeaders
	if clientIPHeaderCfg != "" {
		monitoredHeaders = []string{clientIPHeaderCfg}
	}

	// Walk IP-related headers
	var foundIP instrumentation.NetaddrIP
headersLoop:
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
				break headersLoop
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

func parseIP(s string) instrumentation.NetaddrIP {
	if ip, err := instrumentation.NetaddrParseIP(s); err == nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		if ip, err := instrumentation.NetaddrParseIP(h); err == nil {
			return ip
		}
	}
	return instrumentation.NetaddrIP{}
}

func isGlobal(ip instrumentation.NetaddrIP) bool {
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
