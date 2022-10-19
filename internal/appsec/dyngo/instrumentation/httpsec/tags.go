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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
)

const (
	// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
	envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"
	// envClientIPHeader is the name of the env var used to disable client IP tag collection.
	envClientIPHeaderDisabled = "DD_TRACE_CLIENT_IP_HEADER_DISABLED"
	// multipleIPHeaders sets the multiple ip header tag used internally to tell the backend an error occurred when
	// retrieving an HTTP request client IP.
	multipleIPHeaders = "_dd.multiple-ip-headers"
)

var (
	ipv6SpecialNetworks = []*netaddrIPPrefix{
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
	collectIP      = true
	clientIPHeader string
)

// SetAppSecTags sets the AppSec-specific span tags that are expected to be in
// the web service entry span (span of type `web`) when AppSec is enabled.
func SetAppSecTags(span ddtrace.Span) {
	span.SetTag("_dd.appsec.enabled", 1)
	span.SetTag("_dd.runtime_family", "go")
}

// SetSecurityEventTags sets the AppSec-specific span tags when a security event occurred into the service entry span.
func SetSecurityEventTags(span ddtrace.Span, events []json.RawMessage, remoteIP string, headers, respHeaders map[string][]string) {
	instrumentation.SetEventSpanTags(span, events)
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
func ippref(s string) *netaddrIPPrefix {
	if prefix, err := netaddrParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// SetIPTags sets the IP related span tags for a given request
// See https://docs.datadoghq.com/tracing/configure_data_security#configuring-a-client-ip-header for more information.
func SetIPTags(span ddtrace.Span, r *http.Request) {
	ipHeaders := defaultIPHeaders
	if len(clientIPHeader) > 0 {
		ipHeaders = []string{clientIPHeader}
	}
	var headers []string
	var ips []string
	for _, hdr := range ipHeaders {
		if v := r.Header.Get(hdr); v != "" {
			headers = append(headers, hdr)
			ips = append(ips, v)
		}
	}
	if len(ips) == 0 {
		if remoteIP := parseIP(r.RemoteAddr); remoteIP.IsValid() && isGlobal(remoteIP) {
			span.SetTag(ext.HTTPClientIP, remoteIP.String())
		}
	} else if len(ips) == 1 {
		for _, ipstr := range strings.Split(ips[0], ",") {
			ip := parseIP(strings.TrimSpace(ipstr))
			if ip.IsValid() && isGlobal(ip) {
				span.SetTag(ext.HTTPClientIP, ip.String())
				break
			}
		}
	} else {
		for i := range ips {
			span.SetTag(ext.HTTPRequestHeaders+"."+headers[i], ips[i])
		}
		span.SetTag(multipleIPHeaders, strings.Join(headers, ","))
	}
}

func parseIP(s string) netaddrIP {
	if ip, err := netaddrParseIP(s); err == nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		if ip, err := netaddrParseIP(h); err == nil {
			return ip
		}
	}
	return netaddrIP{}
}

func isGlobal(ip netaddrIP) bool {
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
func init() {
	// Required by sort.SearchStrings
	sort.Strings(collectedHTTPHeaders[:])
	collectIP = collectIP && !internal.BoolEnv(envClientIPHeaderDisabled, false)
	clientIPHeader = os.Getenv(envClientIPHeader)
}
