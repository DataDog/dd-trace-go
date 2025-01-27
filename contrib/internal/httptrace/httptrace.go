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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

var (
	cfg = newConfig()
)

var reportTelemetryConfigOnce sync.Once

type inferredSpanCreatedCtxKey struct{}

type FinishSpanFunc = func(status int, errorFn func(int) bool, opts ...tracer.FinishOption)

// StartRequestSpan starts an HTTP request span with the standard list of HTTP request span tags (http.method, http.url,
// http.useragent). Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, opts ...ddtrace.StartSpanOption) (tracer.Span, context.Context, FinishSpanFunc) {
	// Append our span options before the given ones so that the caller can "overwrite" them.
	// TODO(): rework span start option handling (https://github.com/DataDog/dd-trace-go/issues/1352)

	// we cannot track the configuration in newConfig because it's called during init() and the the telemetry client
	// is not initialized yet
	reportTelemetryConfigOnce.Do(func() {
		telemetry.GlobalClient.ConfigChange([]telemetry.Configuration{
			{Name: "inferred_proxy_services_enabled", Value: cfg.inferredProxyServicesEnabled},
		})
		log.Debug("internal/httptrace: telemetry.ConfigChange called with cfg: %v:", cfg)
	})

	var ipTags map[string]string
	if cfg.traceClientIP {
		ipTags, _ = httpsec.ClientIPTags(r.Header, true, r.RemoteAddr)
	}

	nopts := make([]ddtrace.StartSpanOption, 0, len(opts)+1+len(ipTags))

	var inferredProxySpan tracer.Span

	if cfg.inferredProxyServicesEnabled {
		inferredProxySpanCreated := false

		if created, ok := r.Context().Value(inferredSpanCreatedCtxKey{}).(bool); ok {
			inferredProxySpanCreated = created
		}

		if !inferredProxySpanCreated {
			var inferredStartSpanOpts []ddtrace.StartSpanOption

			requestProxyContext, err := extractInferredProxyContext(r.Header)
			if err != nil {
				log.Debug(err.Error())
			} else {
				spanParentCtx, spanParentErr := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
				if spanParentErr == nil {
					if spanLinksCtx, spanLinksOk := spanParentCtx.(ddtrace.SpanContextWithLinks); spanLinksOk {
						inferredStartSpanOpts = append(inferredStartSpanOpts, tracer.WithSpanLinks(spanLinksCtx.SpanLinks()))
					}
				}
				inferredProxySpan = startInferredProxySpan(requestProxyContext, spanParentCtx, inferredStartSpanOpts...)
			}
		}
	}

	nopts = append(nopts,
		func(ssCfg *ddtrace.StartSpanConfig) {
			if ssCfg.Tags == nil {
				ssCfg.Tags = make(map[string]interface{})
			}
			ssCfg.Tags[ext.SpanType] = ext.SpanTypeWeb
			ssCfg.Tags[ext.HTTPMethod] = r.Method
			ssCfg.Tags[ext.HTTPURL] = UrlFromRequest(r, cfg.queryString)
			ssCfg.Tags[ext.HTTPUserAgent] = r.UserAgent()
			ssCfg.Tags["_dd.measured"] = 1
			if r.Host != "" {
				ssCfg.Tags["http.host"] = r.Host
			}

			if inferredProxySpan != nil {
				tracer.ChildOf(inferredProxySpan.Context())(ssCfg)
			} else {
				spanParentCtx, spanParentErr := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
				if spanParentErr == nil {
					if spanLinksCtx, spanLinksOk := spanParentCtx.(ddtrace.SpanContextWithLinks); spanLinksOk {
						tracer.WithSpanLinks(spanLinksCtx.SpanLinks())(ssCfg)
					}
					tracer.ChildOf(spanParentCtx)(ssCfg)
				}
			}

			for k, v := range ipTags {
				ssCfg.Tags[k] = v
			}
		})

	nopts = append(nopts, opts...)

	var requestContext = r.Context()
	if inferredProxySpan != nil {
		requestContext = context.WithValue(r.Context(), inferredSpanCreatedCtxKey{}, true)
	}

	span, ctx := tracer.StartSpanFromContext(requestContext, namingschema.OpName(namingschema.HTTPServer), nopts...)
	return span, ctx, func(status int, errorFn func(int) bool, opts ...tracer.FinishOption) {
		FinishRequestSpan(span, status, errorFn, opts...)
		if inferredProxySpan != nil {
			FinishRequestSpan(inferredProxySpan, status, errorFn, opts...)
		}
	}
}

// FinishRequestSpan finishes the given HTTP request span and sets the expected response-related tags such as the status
// code. If not nil, errorFn will override the isStatusError method on httptrace for determining error codes. Any further span finish option can be added with opts.
func FinishRequestSpan(s tracer.Span, status int, errorFn func(int) bool, opts ...tracer.FinishOption) {
	var statusStr string
	var fn func(int) bool
	if errorFn == nil {
		fn = cfg.isStatusError
	} else {
		fn = errorFn
	}
	// if status is 0, treat it like 200 unless 0 was called out in DD_TRACE_HTTP_SERVER_ERROR_STATUSES
	if status == 0 {
		if fn(status) {
			statusStr = "0"
			s.SetTag(ext.Error, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
		} else {
			statusStr = "200"
		}
	} else {
		statusStr = strconv.Itoa(status)
		if fn(status) {
			s.SetTag(ext.Error, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
		}
	}
	s.SetTag(ext.HTTPCode, statusStr)
	s.Finish(opts...)
}

// UrlFromRequest returns the full URL from the HTTP request. If queryString is true, params are collected and they are obfuscated either by the default query string obfuscator or the custom obfuscator provided by the user (through DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP)
// See https://docs.datadoghq.com/tracing/configure_data_security/?tab=net#redact-query-strings for more information.
func UrlFromRequest(r *http.Request, queryString bool) string {
	// Quoting net/http comments about net.Request.URL on server requests:
	// "For most requests, fields other than Path and RawQuery will be
	// empty. (See RFC 7230, Section 5.3)"
	// This is why we can't rely entirely on url.URL.String(), url.URL.Host, url.URL.Scheme, etc...
	var url string
	path := r.URL.EscapedPath()
	scheme := "http"
	if s := r.URL.Scheme; s != "" {
		scheme = s
	} else if r.TLS != nil {
		scheme = "https"
	}
	if r.Host != "" {
		url = strings.Join([]string{scheme, "://", r.Host, path}, "")
	} else {
		url = path
	}
	// Collect the query string if we are allowed to report it and obfuscate it if possible/allowed
	if queryString && r.URL.RawQuery != "" {
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
