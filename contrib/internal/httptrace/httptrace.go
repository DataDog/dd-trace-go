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
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

var (
	cfg = newConfig()
)

// StartRequestSpan starts an HTTP request span with the standard list of HTTP request span tags (http.method, http.url,
// http.useragent). Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, opts ...ddtrace.StartSpanOption) (tracer.Span, context.Context) {
	// Append our span options before the given ones so that the caller can "overwrite" them.
	// TODO(): rework span start option handling (https://github.com/DataDog/dd-trace-go/issues/1352)

	var ipTags map[string]string
	if cfg.traceClientIP {
		ipTags, _ = httptrace.ClientIPTags(r.Header, true, r.RemoteAddr)
	}
	nopts := make([]ddtrace.StartSpanOption, 0, len(opts)+1+len(ipTags))
	nopts = append(nopts,
		func(cfg *ddtrace.StartSpanConfig) {
			if cfg.Tags == nil {
				cfg.Tags = make(map[string]interface{})
			}
			cfg.Tags[ext.SpanType] = ext.SpanTypeWeb
			cfg.Tags[ext.HTTPMethod] = r.Method
			cfg.Tags[ext.HTTPURL] = urlFromRequest(r)
			cfg.Tags[ext.HTTPUserAgent] = r.UserAgent()
			cfg.Tags["_dd.measured"] = 1
			if r.Host != "" {
				cfg.Tags["http.host"] = r.Host
			}
			if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
				cfg.Parent = spanctx
			}
			for k, v := range ipTags {
				cfg.Tags[k] = v
			}
		})
	nopts = append(nopts, opts...)
	return tracer.StartSpanFromContext(r.Context(), namingschema.OpName(namingschema.HTTPServer), nopts...)
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

// HeaderTagsFromRequest matches req headers to user-defined list of header tags
// and creates span tags based on the header tag target and the req header value
func HeaderTagsFromRequest(req *http.Request, headerCfg *internal.LockMap) ddtrace.StartSpanOption {
	var tags []struct {
		key string
		val string
	}

	headerCfg.Iter(func(header, tag string) {
		if vs, ok := req.Header[header]; ok {
			tags = append(tags, struct {
				key string
				val string
			}{tag, strings.TrimSpace(strings.Join(vs, ","))})
		}
	})

	return func(cfg *ddtrace.StartSpanConfig) {
		for _, t := range tags {
			cfg.Tags[t.key] = t.val
		}
	}
}
