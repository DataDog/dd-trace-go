// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package clientip

import (
	"net/netip"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
const envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"

var defaultIPHeaders = []string{
	"x-forwarded-for",
	"x-real-ip",
	"true-client-ip",
	"x-client-ip",
	"forwarded",
	"forwarded-for",
	"x-cluster-client-ip",
	"fastly-client-ip",
	"cf-connecting-ip",
	"cf-connecting-ipv6",
}

var (
	// monitoredHeaders is the list of IP-related headers leveraged to retrieve the
	// public client IP address in RemoteAddr. This is defined at init time in
	// function of the value of the envClientIPHeader environment variable.
	monitoredHeaders []string

	// customHeaderConfigured records that envClientIPHeader named a header, and
	// that the header is therefore the only place identity may come from.
	customHeaderConfigured bool
)

func init() {
	readMonitoredHeadersConfig()
}

func readMonitoredHeadersConfig() {
	if header := env.Get(envClientIPHeader); header != "" {
		// Make this header the only one to consider in RemoteAddr
		monitoredHeaders = []string{header}
		customHeaderConfigured = true
	} else {
		// No specific IP header was configured, use the default list
		monitoredHeaders = defaultIPHeaders
		customHeaderConfigured = false
	}
}

// AppendDefaultHeaders appends the default IP headers in resolution order.
func AppendDefaultHeaders(headers []string) []string {
	return append(headers, defaultIPHeaders...)
}

// MonitoredHeaders returns the configured headers in resolution order. Callers
// must not modify the returned slice.
func MonitoredHeaders() []string {
	return monitoredHeaders
}

// ResetConfig re-reads DD_TRACE_CLIENT_IP_HEADER. The configuration is read once
// at init; this exists so tests in the packages that consume the policy can
// exercise both branches, mirroring httptrace.ResetCfg.
func ResetConfig() {
	readMonitoredHeadersConfig()
}

// CustomHeaderConfigured reports whether DD_TRACE_CLIENT_IP_HEADER named the
// header client identity must be taken from.
//
// An integration that determines identity from its own infrastructure has to
// defer to the default policy when this is true. Naming a header is an explicit
// statement by the operator about where identity lives — typically because
// something in front of them, a CDN say, is the only thing that knows the real
// client — and it outranks an address an integration inferred.
func CustomHeaderConfigured() bool {
	return customHeaderConfigured
}

// Resolve returns the remote IP and the client IP of a request, applying the
// default resolution policy over [MonitoredHeaders].
//
// An invalid returned address means no identity could be determined from the
// data given, and produces no span tag.
func Resolve(hdrs map[string][]string, hasCanonicalHeaders bool, remoteAddr string) (remoteIP, clientIP netip.Addr) {
	return resolveWith(hdrs, hasCanonicalHeaders, remoteAddr, monitoredHeaders)
}

// TagsFor returns the Datadog span tags `http.client_ip` containing the client
// IP and `network.client.ip` containing the parsed remote address. Each tag is
// present only if the corresponding address is valid; nil is returned when
// neither is.
func TagsFor(remoteAddr string, clientIP netip.Addr) map[string]string {
	remoteIP := parseIP(remoteAddr)
	remoteIPValid := remoteIP.IsValid()
	clientIPValid := clientIP.IsValid()

	if !remoteIPValid && !clientIPValid {
		return nil
	}

	tags := make(map[string]string, 2)
	if remoteIPValid {
		tags[ext.NetworkClientIP] = remoteIP.String()
	}
	if clientIPValid {
		tags[ext.HTTPClientIP] = clientIP.String()
	}

	return tags
}
