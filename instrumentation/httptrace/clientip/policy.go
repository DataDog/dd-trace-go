// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clientip

import (
	"net/netip"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
const envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"

// DefaultIPHeaders is the default list of IP-related headers leveraged to
// retrieve the public client IP address in RemoteAddr. The headers are
// checked in the order they are listed; do not re-order unless you know what
// you are doing.
var DefaultIPHeaders = []string{
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

// monitoredHeaders is the list of IP-related headers leveraged to retrieve the
// public client IP address in RemoteAddr. This is defined at init time in
// function of the value of the envClientIPHeader environment variable.
var monitoredHeaders []string

func init() {
	readMonitoredHeadersConfig()
}

func readMonitoredHeadersConfig() {
	if header := env.Get(envClientIPHeader); header != "" {
		// Make this header the only one to consider in RemoteAddr
		monitoredHeaders = []string{header}
	} else {
		// No specific IP header was configured, use the default list
		monitoredHeaders = DefaultIPHeaders
	}
}

// MonitoredHeaders returns the IP-related headers the default resolution policy
// scans, which is either the single header named by DD_TRACE_CLIENT_IP_HEADER or
// [DefaultIPHeaders]. Callers must not modify the returned slice.
func MonitoredHeaders() []string {
	return monitoredHeaders
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
// IP and `network.client.ip` containing the remote IP. Each tag is present only
// if the corresponding address is valid; nil is returned when neither is.
func TagsFor(remoteIP netip.Addr, clientIP netip.Addr) map[string]string {
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
