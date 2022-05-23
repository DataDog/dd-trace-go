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
