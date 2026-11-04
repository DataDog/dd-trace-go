// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// ServerSpanName returns the OpenTelemetry HTTP server span name for method and
// optional low-cardinality route. Unknown methods use HTTP as the method token.
func ServerSpanName(method, route string) string {
	_, methodToken, _ := NormalizeHTTPMethod(method)
	if route == "" {
		return methodToken
	}
	return methodToken + " " + route
}

func setOTelServerRequestTags(tags map[string]any, r *http.Request, ipTags map[string]string) {
	method, methodToken, originalMethod := NormalizeHTTPMethod(r.Method)
	tags[ext.HTTPRequestMethod] = method
	if originalMethod != "" {
		tags[ext.HTTPRequestMethodOriginal] = originalMethod
	}
	tags[ext.ResourceName] = methodToken
	tags[ext.SpanKind] = ext.SpanKindServer

	tags[ext.URLScheme] = serverScheme(r)
	if r.URL != nil {
		if r.URL.Path != "" {
			tags[ext.URLPath] = r.URL.Path
		}
		if query := queryStringFromRequest(r, cfg.queryString, false); query != "" {
			tags[ext.URLQuery] = query
		}
	}

	address, port := serverAddressPort(r)
	if address != "" {
		tags[ext.ServerAddress] = address
		if port > 0 {
			tags[ext.ServerPort] = port
		}
	}
	if userAgent := r.UserAgent(); userAgent != "" {
		tags[ext.UserAgentOriginal] = userAgent
	}
	if clientAddress := ipTags[ext.HTTPClientIP]; clientAddress != "" {
		tags[ext.ClientAddress] = clientAddress
	}
	if peerAddress := ipTags[ext.NetworkClientIP]; peerAddress != "" {
		tags[ext.NetworkPeerAddress] = peerAddress
	}
}

func serverScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func serverAddressPort(r *http.Request) (string, int) {
	authority := r.Host
	if r.URL != nil && r.URL.Host != "" {
		authority = r.URL.Host
	}

	parsed := parseHTTPAuthority(authority)
	if !parsed.valid {
		return "", -1
	}

	port := parsed.port
	if (port == 80 && r.TLS == nil) || (port == 443 && r.TLS != nil) {
		port = -1
	}
	return parsed.address, port
}
