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
	"os"
	"strconv"
	"strings"

	"inet.af/netaddr"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	ipv6SpecialNetworks = []*netaddr.IPPrefix{
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
	clientIPHeader = os.Getenv("DD_TRACE_CLIENT_IP_HEADER")
	collectIP      = os.Getenv("DD_TRACE_CLIENT_IP_HEADER_DISABLED") != "true"
)

// StartRequestSpan starts an HTTP request span with the standard list of HTTP request span tags (http.method, http.url,
// http.useragent). Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, opts ...ddtrace.StartSpanOption) (tracer.Span, context.Context) {
	// Append our span options before the given ones so that the caller can "overwrite" them.
	// TODO(): rework span start option handling (https://github.com/DataDog/dd-trace-go/issues/1352)
	opts = append([]ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, r.URL.Path),
		tracer.Tag(ext.HTTPUserAgent, r.UserAgent()),
		tracer.Measured(),
	}, opts...)
	if r.Host != "" {
		opts = append([]ddtrace.StartSpanOption{
			tracer.Tag("http.host", r.Host),
		}, opts...)
	}
	if collectIP {
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
func ippref(s string) *netaddr.IPPrefix {
	if prefix, err := netaddr.ParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// genClientIPSpanTags generates the client IP related tags that need to be added to the span.
// See https://datadoghq.atlassian.net/wiki/spaces/APS/pages/2118779066/Client+IP+addresses+resolution
func genClientIPSpanTags(r *http.Request) []ddtrace.StartSpanOption {
	ipHeaders := defaultIPHeaders
	if len(clientIPHeader) > 0 {
		ipHeaders = []string{clientIPHeader}
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
		opts = append(opts, tracer.Tag(ext.MultipleIPHeaders, strings.Join(headers, ",")))
	}
	return opts
}

func parseIP(s string) netaddr.IP {
	if ip, err := netaddr.ParseIP(s); err == nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		if ip, err := netaddr.ParseIP(h); err == nil {
			return ip
		}
	}
	return netaddr.IP{}
}

func isGlobal(ip netaddr.IP) bool {
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
