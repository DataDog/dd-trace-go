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
	if ip := getClientIP(r.RemoteAddr, r.Header, clientIPHeader); ip.IsValid() {
		opts = append(opts, tracer.Tag(ext.HTTPClientIP, ip.String()))
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

// Helper function to return the IP network out of a string.
func ippref(s string) *netaddr.IPPrefix {
	if prefix, err := netaddr.ParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

// getClientIP uses the request headers to resolve the client IP. If a specific header to check is provided through
// DD_CLIENT_IP_HEADER, then only this header is checked.
func getClientIP(remoteAddr string, headers http.Header, clientIPHeader string) netaddr.IP {
	ipHeaders := defaultIPHeaders
	if len(clientIPHeader) > 0 {
		ipHeaders = []string{clientIPHeader}
	}
	check := func(value string) netaddr.IP {
		for _, ipstr := range strings.Split(value, ",") {
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
	for _, hdr := range ipHeaders {
		if value := headers.Get(hdr); value != "" {
			if ip := check(value); ip.IsValid() {
				return ip
			}
		}
	}

	if remoteIP := parseIP(remoteAddr); remoteIP.IsValid() && isGlobal(remoteIP) {
		return remoteIP
	}
	return netaddr.IP{}
}

func parseIP(s string) netaddr.IP {
	ip, err := netaddr.ParseIP(s)
	if err != nil {
		h, _ := splitHostPort(s)
		ip, err = netaddr.ParseIP(h)
	}
	return ip
}

func isGlobal(ip netaddr.IP) bool {
	if ip.Is6() {
		for _, network := range ipv6SpecialNetworks {
			if network.Contains(ip) {
				return false
			}
		}
	}
	//IsPrivate also checks for ipv6 ULA
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

// SplitHostPort splits a network address of the form `host:port` or
// `[host]:port` into `host` and `port`.
func splitHostPort(addr string) (host string, port string) {
	i := strings.LastIndex(addr, "]:")
	if i != -1 {
		// ipv6
		return strings.Trim(addr[:i+1], "[]"), addr[i+2:]
	}

	i = strings.LastIndex(addr, ":")
	if i == -1 {
		// not an address with a port number
		return addr, ""
	}
	return addr[:i], addr[i+1:]
}
