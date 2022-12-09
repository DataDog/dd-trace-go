// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"encoding/json"
	"net"
	"net/http"
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
	// tagMultipleIPHeaders sets the multiple ip header tag used internally to tell the backend an error occurred when
	// retrieving an HTTP request client IP.
	tagMultipleIPHeaders = "_dd.multiple-ip-headers"
	// tagBlockedRequest used to convey whether a request is blocked
	tagBlockedRequest = "appsec.blocked"
)

var (
	ipv6SpecialNetworks = []*instrumentation.NetaddrIPPrefix{
		ippref("fec0::/10"), // site local
	}
	defaultIPHeaders = []string{
		"x-forwarded-for",
		"x-real-ip",
		"x-client-ip",
		"x-forwarded",
		"x-cluster-client-ip",
		"forwarded-for",
		"forwarded",
		"via",
		"true-client-ip",
	}
	// List of HTTP headers we collect and send.
	collectedHTTPHeaders = append(defaultIPHeaders,
		"host",
		"content-length",
		"content-type",
		"content-encoding",
		"content-language",
		"forwarded",
		"user-agent",
		"accept",
		"accept-encoding",
		"accept-language")
	clientIPHeader string
)

func init() {
	// Required by sort.SearchStrings
	sort.Strings(collectedHTTPHeaders[:])
	clientIPHeader = os.Getenv(envClientIPHeader)
}

// SetSecurityEventTags sets the AppSec-specific span tags when a security event occurred into the service entry span.
func SetSecurityEventTags(span instrumentation.TagSetter, events []json.RawMessage, remoteIP string, headers, respHeaders map[string][]string) {
	if err := instrumentation.SetEventSpanTags(span, events); err != nil {
		log.Error("appsec: unexpected error while creating the appsec event tags: %v", err)
	}
	span.SetTag("network.client.ip", remoteIP)
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

// SetIPTags sets the IP related span tags for a given request
func SetIPTags(s instrumentation.TagSetter, r *http.Request) {
	for k, v := range IPTagsFromHeaders(r.Header, r.RemoteAddr) {
		s.SetTag(k, v)
	}

}

// IPTagsFromHeaders generates the IP related span tags for a given request's headers
// See https://docs.datadoghq.com/tracing/configure_data_security#configuring-a-client-ip-header for more information.
func IPTagsFromHeaders(hdrs map[string][]string, remoteAddr string) map[string]string {
	tags := map[string]string{}
	ipHeaders := defaultIPHeaders
	if len(clientIPHeader) > 0 {
		ipHeaders = []string{clientIPHeader}
	}
	var (
		headers []string
		ips     []string
	)

	// Make sure all headers are lower-case
	for k, v := range hdrs {
		hdrs[strings.ToLower(k)] = v
	}
	for _, hdr := range ipHeaders {
		if v := hdrs[hdr]; len(v) >= 1 && v[0] != "" {
			headers = append(headers, hdr)
			ips = append(ips, v[0])
		}
	}

	var privateIP instrumentation.NetaddrIP
	// First try to use the IP from the headers if only one IP was found
	if l := len(ips); l == 1 {
		for _, ipstr := range strings.Split(ips[0], ",") {
			ip := parseIP(strings.TrimSpace(ipstr))
			if ip.IsValid() {
				if isGlobal(ip) {
					tags[ext.HTTPClientIP] = ip.String()
					return tags
				} else if !privateIP.IsValid() {
					privateIP = ip
				}
			}
		}
	} else if l > 1 { // If more than one IP header, report them
		for i := range ips {
			tags[ext.HTTPRequestHeaders+"."+headers[i]] = ips[i]
		}
		tags[tagMultipleIPHeaders] = strings.Join(headers, ",")
	}

	// Try to get a global IP from remoteAddr. If not, try to use any private IP we found
	if remoteIP := parseIP(remoteAddr); remoteIP.IsValid() && (isGlobal(remoteIP) || !privateIP.IsValid()) {
		tags[ext.HTTPClientIP] = remoteIP.String()
	} else if privateIP.IsValid() {
		tags[ext.HTTPClientIP] = privateIP.String()
	}

	return tags
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
	isGlobal := !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
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
