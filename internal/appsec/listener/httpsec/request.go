// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"net/netip"
	"os"
	"strings"

	"github.com/DataDog/appsec-internal-go/httpsec"
)

const (
	// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
	envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"
)

var (
	// defaultIPHeaders is the default list of IP-related headers leveraged to
	// retrieve the public client IP address in RemoteAddr.
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
		"cf-connecting-ipv6",
	}

	// defaultCollectedHeaders is the default list of HTTP headers collected as
	// request span tags when appsec is enabled.
	defaultCollectedHeaders = append([]string{
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
		"accept-language",
		"x-amzn-trace-id",
		"cloudfront-viewer-ja3-fingerprint",
		"cf-ray",
		"x-cloud-trace-context",
		"x-appgw-trace-id",
		"akamai-user-risk",
		"x-sigsci-requestid",
		"x-sigsci-tags",
	}, defaultIPHeaders...)

	// collectedHeadersLookupMap is a helper lookup map of HTTP headers to
	// collect as request span tags when appsec is enabled. It is computed at
	// init-time based on defaultCollectedHeaders and leveraged by NormalizeHTTPHeaders.
	collectedHeadersLookupMap map[string]struct{}

	// monitoredClientIPHeadersCfg is the list of IP-related headers leveraged to
	// retrieve the public client IP address in RemoteAddr. This is defined at init
	// time in function of the value of the envClientIPHeader environment variable.
	monitoredClientIPHeadersCfg []string
)

// ClientIPTags returns the resulting Datadog span tags `http.client_ip`
// containing the client IP and `network.client.ip` containing the remote IP.
// The tags are present only if a valid ip address has been returned by
// RemoteAddr().
func ClientIPTags(headers map[string][]string, hasCanonicalHeaders bool, remoteAddr string) (tags map[string]string, clientIP netip.Addr) {
	remoteIP, clientIP := httpsec.ClientIP(headers, hasCanonicalHeaders, remoteAddr, monitoredClientIPHeadersCfg)
	tags = httpsec.ClientIPTags(remoteIP, clientIP)
	return tags, clientIP
}

func init() {
	makeCollectedHTTPHeadersLookupMap()
	readMonitoredClientIPHeadersConfig()
}

func makeCollectedHTTPHeadersLookupMap() {
	collectedHeadersLookupMap = make(map[string]struct{}, len(defaultCollectedHeaders))
	for _, h := range defaultCollectedHeaders {
		collectedHeadersLookupMap[h] = struct{}{}
	}
}

func normalizeHTTPHeaderName(name string) string {
	return strings.ToLower(name)
}

func readMonitoredClientIPHeadersConfig() {
	if header := os.Getenv(envClientIPHeader); header != "" {
		// Make this header the only one to consider in RemoteAddr
		monitoredClientIPHeadersCfg = []string{header}

		// Add this header to the list of collected headers
		header = normalizeHTTPHeaderName(header)
		collectedHeadersLookupMap[header] = struct{}{}
	} else {
		// No specific IP header was configured, use the default list
		monitoredClientIPHeadersCfg = defaultIPHeaders
	}
}
