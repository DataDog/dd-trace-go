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
	"strconv"
	"strings"

	"inet.af/netaddr"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// List of standard HTTP request span tags.
const (
	// HTTPMethod is the HTTP request method.
	HTTPMethod = ext.HTTPMethod
	// HTTPURL is the full HTTP request URL in the form `scheme://host[:port]/path[?query][#fragment]`.
	HTTPURL = ext.HTTPURL
	// HTTPUserAgent is the user agent header value of the HTTP request.
	HTTPUserAgent = "http.useragent"
	// HTTPCode is the HTTP response status code sent by the HTTP request handler.
	HTTPCode = ext.HTTPCode
)

// StartRequestSpan starts an HTTP request span with the standard list of HTTP request span tags. URL query parameters
// are added to the URL tag when queryParams is true. Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, queryParams bool, opts ...ddtrace.StartSpanOption) (tracer.Span, context.Context) {
	opts = append([]ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(HTTPMethod, r.Method),
		tracer.Tag(HTTPURL, makeURLTag(r, queryParams)),
		tracer.Tag(HTTPUserAgent, r.UserAgent()),
		tracer.Measured(),
	}, opts...)
	if r.URL.Host != "" {
		opts = append([]ddtrace.StartSpanOption{
			tracer.Tag("http.host", r.URL.Host),
		}, opts...)
	}
	if ip := getClientIP(r.RemoteAddr, r.Header, cfg.ipHeader); ip.IsValid() {
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
	s.SetTag(HTTPCode, statusStr)
	if status >= 500 && status < 600 {
		s.SetTag(ext.Error, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
	}
	s.Finish(opts...)
}

// Create the http.url value out of the given HTTP request.
func makeURLTag(r *http.Request, queryParams bool) string {
	var u strings.Builder
	u.WriteString(r.URL.EscapedPath())
	if query := r.URL.RawQuery; queryParams && query != "" {
		u.WriteByte('?')
		u.WriteString(query)
	}
	return u.String()
}

// Helper function to return the IP network out of a string.
func ippref(s string) *netaddr.IPPrefix {
	if prefix, err := netaddr.ParseIPPrefix(s); err == nil {
		return &prefix
	}
	return nil
}

var (
	ipv6SpecialNetworks = []*netaddr.IPPrefix{
		ippref("fec0::/10"), // site local
	}

	headerList = []string{"x-forwarded-for", "x-real-ip", "x-client-ip", "x-forwarded", "x-cluster-client-ip", "forwarded-for", "forwarded", "via", "true-client-ip"}
)

func getClientIP(remoteAddr string, headers http.Header, usrHeader string) netaddr.IP {
	if len(usrHeader) > 0 {
		headerList = []string{usrHeader}
	}
	check := func(value string) netaddr.IP {
		for _, ip := range strings.Split(value, ",") {
			ipStr := strings.Trim(ip, " ")
			ip := parseIP(ipStr)

			if !ip.IsValid() {
				continue
			}

			if isGlobal(ip) {
				return ip
			}
		}
		return netaddr.IP{}
	}

	for _, key := range headerList {
		if value := headers.Get(key); value != "" {
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
