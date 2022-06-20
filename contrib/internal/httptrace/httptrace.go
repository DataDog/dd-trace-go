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

// genClientIPSpanTags generates the client IP related span tags.
func genClientIPSpanTags(r *http.Request) []ddtrace.StartSpanOption {
	tags := []ddtrace.StartSpanOption{}
	ip, matches := getClientIP(r)
	if matches == nil {
		if ip.IsValid() {
			tags = append(tags, tracer.Tag(ext.HTTPClientIP, ip.String()))
		}
		return tags
	}
	sb := strings.Builder{}
	for _, hdr := range matches {
		tags = append(tags, tracer.Tag(ext.HTTPRequestHeaders+"."+hdr, ip))
		sb.WriteString(hdr + ",")
	}
	hdrList := sb.String()
	tags = append(tags, tracer.Tag(ext.MultipleIPHeaders, hdrList[:len(hdrList)-1]))
	return tags
}

// getClientIP attempts to find the client IP address in the given request r.
// If several IP headers are present in the request, the returned IP is invalid and the list of all IP headers is
// returned. Otherwise, the returned list is nil.
// See https://datadoghq.atlassian.net/wiki/spaces/APS/pages/2118779066/Client+IP+addresses+resolution
func getClientIP(r *http.Request) (netaddr.IP, []string) {
	ipHeaders := defaultIPHeaders
	if len(clientIPHeader) > 0 {
		ipHeaders = []string{clientIPHeader}
	}
	check := func(s string) netaddr.IP {
		for _, ipstr := range strings.Split(s, ",") {
			ip := parseIP(strings.TrimSpace(ipstr))
			if !ip.IsValid() {
				continue
			}
			if isGlobal(ip) {
				return ip
			}
		}
		return netaddr.IP{}
	}
	matches := []string{}
	var matchedIP netaddr.IP
	for _, hdr := range ipHeaders {
		if v := r.Header.Get(hdr); v != "" {
			matches = append(matches, hdr)
			if ip := check(v); ip.IsValid() {
				matchedIP = ip
			}
		}
	}
	if len(matches) == 1 {
		return matchedIP, nil
	}
	if len(matches) > 1 {
		return netaddr.IP{}, matches
	}
	if remoteIP := parseIP(r.RemoteAddr); remoteIP.IsValid() && isGlobal(remoteIP) {
		return remoteIP, nil
	}
	return netaddr.IP{}, nil
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
