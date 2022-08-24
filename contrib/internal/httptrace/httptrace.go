// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptrace provides functionalities to trace HTTP requests that are commonly required and used across
// contrib/** integrations.
package httptrace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
	cfg = newConfig()
)

// multipleIPHeaders sets the multiple ip header tag used internally to tell the backend an error occurred when
// retrieving an HTTP request client IP.
const multipleIPHeaders = "_dd.multiple-ip-headers"

// StartRequestSpan starts an HTTP request span with the standard list of HTTP request span tags (http.method, http.url,
// http.useragent). Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, opts ...ddtrace.StartSpanOption) (tracer.Span, context.Context) {
	// Append our span options before the given ones so that the caller can "overwrite" them.
	// TODO(): rework span start option handling (https://github.com/DataDog/dd-trace-go/issues/1352)
	opts = append([]ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, urlFromRequest(r)),
		tracer.Tag(ext.HTTPUserAgent, r.UserAgent()),
		tracer.Measured(),
	}, opts...)
	if r.Host != "" {
		opts = append([]ddtrace.StartSpanOption{
			tracer.Tag("http.host", r.Host),
		}, opts...)
	}
	if cfg.clientIP {
		opts = append(genClientIPSpanTags(r), opts...)
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	return tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
}

// FinishRequestSpan finishes the given HTTP request span and sets the expected response-related tags such as the status
// code. Any further span finish option can be added with opts.
func FinishRequestSpan(s tracer.Span, status int, opts ...tracer.FinishOption) {
	var statusStr string
	if status == 0 {
		statusStr = "200"
	} else {
		statusStr = strconv.Itoa(status)
	}
	s.SetTag(ext.HTTPCode, statusStr)
	if status >= 500 && status < 600 {
		s.SetTag(ext.Error, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
	}
	s.Finish(opts...)
}

// ippref returns the IP network from an IP address string s. If not possible, it returns nil.
func ippref(s string) *netaddrIPPrefix {
	if prefix, err := netaddrParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// genClientIPSpanTags generates the client IP related tags that need to be added to the span.
// See https://docs.datadoghq.com/tracing/configure_data_security#configuring-a-client-ip-header for more information.
func genClientIPSpanTags(r *http.Request) []ddtrace.StartSpanOption {
	ipHeaders := defaultIPHeaders
	if len(cfg.clientIPHeader) > 0 {
		ipHeaders = []string{cfg.clientIPHeader}
	}
	var headers []string
	var ips []string
	var opts []ddtrace.StartSpanOption
	for _, hdr := range ipHeaders {
		if v := r.Header.Get(hdr); v != "" {
			headers = append(headers, hdr)
			ips = append(ips, v)
		}
	}
	if len(ips) == 0 {
		if remoteIP := parseIP(r.RemoteAddr); remoteIP.IsValid() && isGlobal(remoteIP) {
			opts = append(opts, tracer.Tag(ext.HTTPClientIP, remoteIP.String()))
		}
	} else if len(ips) == 1 {
		for _, ipstr := range strings.Split(ips[0], ",") {
			ip := parseIP(strings.TrimSpace(ipstr))
			if ip.IsValid() && isGlobal(ip) {
				opts = append(opts, tracer.Tag(ext.HTTPClientIP, ip.String()))
				break
			}
		}
	} else {
		for i := range ips {
			opts = append(opts, tracer.Tag(ext.HTTPRequestHeaders+"."+headers[i], ips[i]))
		}
		opts = append(opts, tracer.Tag(multipleIPHeaders, strings.Join(headers, ",")))
	}
	return opts
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

// urlFromRequest returns the full URL from the HTTP request. If query params are collected, they are obfuscated granted
// obfuscation is not disabled by the user (through DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP)
// See https://docs.datadoghq.com/tracing/configure_data_security#redacting-the-query-in-the-url for more information.
func urlFromRequest(r *http.Request) string {
	// Quoting net/http comments about net.Request.URL on server requests:
	// "For most requests, fields other than Path and RawQuery will be
	// empty. (See RFC 7230, Section 5.3)"
	// This is why we don't rely on url.URL.String(), url.URL.Host, url.URL.Scheme, etc...
	var url string
	path := r.URL.EscapedPath()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Host != "" {
		url = strings.Join([]string{scheme, "://", r.Host, path}, "")
	} else {
		url = path
	}
	// Collect the query string if we are allowed to report it and obfuscate it if possible/allowed
	if cfg.queryString && r.URL.RawQuery != "" {
		query := r.URL.RawQuery
		if cfg.queryStringRegexp != nil {
			query = cfg.queryStringRegexp.ReplaceAllLiteralString(query, "<redacted>")
		}
		url = strings.Join([]string{url, query}, "?")
	}
	if frag := r.URL.EscapedFragment(); frag != "" {
		url = strings.Join([]string{url, frag}, "#")
	}
	return url
}
