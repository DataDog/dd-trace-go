// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"net/http"
	"net/netip"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/clientip"
)

var (
	// defaultCollectedHeaders is the default list of HTTP headers collected as
	// request span tags when appsec is enabled.
	defaultCollectedHeaders = clientip.AppendDefaultHeaders([]string{
		"accept-encoding",
		"accept-language",
		"accept",
		"akamai-user-risk",
		"cf-ray",
		"cloudfront-viewer-ja3-fingerprint",
		"content-encoding",
		"content-language",
		"content-length",
		"content-type",
		"host",
		"user-agent",
		"via",
		"x-amzn-trace-id",
		"x-appgw-trace-id",
		"x-cloud-trace-context",
		"x-forwarded",
		"x-sigsci-requestid",
		"x-sigsci-tags",
	})

	// collectedHeadersLookupMap is a helper lookup map of HTTP headers to
	// collect as request span tags when appsec is enabled. It is computed at
	// init-time based on defaultCollectedHeaders and leveraged by NormalizeHTTPHeaders.
	collectedHeadersLookupMap map[string]struct{}
)

// ClientIPTags resolves the client identity from raw transport data with the
// default policy and returns it as span tags.
//
// HTTP integrations resolve identity once at the instrumentation boundary and
// carry it in the operation arguments, so they do not call this. It exists for
// the gRPC listener, which still resolves from the raw request metadata.
func ClientIPTags(headers map[string][]string, hasCanonicalHeaders bool, remoteAddr string) (tags map[string]string, clientIP netip.Addr) {
	_, clientIP = clientip.Resolve(headers, hasCanonicalHeaders, remoteAddr)
	return clientip.TagsFor(remoteAddr, clientIP), clientIP
}

// NormalizeHTTPHeaders returns the HTTP headers following Datadog's
// normalization format.
func NormalizeHTTPHeaders(headers map[string][]string) (normalized map[string]string) {
	if len(headers) == 0 {
		return nil
	}
	normalized = make(map[string]string, len(collectedHeadersLookupMap))
	for k, v := range headers {
		k = normalizeHTTPHeaderName(k)
		if _, found := collectedHeadersLookupMap[k]; found {
			normalized[k] = normalizeHTTPHeaderValue(v)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// Remove cookies from the request headers and return the map of headers
// Used from `server.request.headers.no_cookies` and server.response.headers.no_cookies` addresses for the WAF
func headersRemoveCookies(headers http.Header) map[string][]string {
	headersNoCookies := make(http.Header, len(headers))
	for k, v := range headers {
		k := strings.ToLower(k)
		if k == "cookie" {
			continue
		}
		headersNoCookies[k] = v
	}
	return headersNoCookies
}

func headersToLower(headers map[string][]string) map[string][]string {
	headersNoCookies := make(map[string][]string, len(headers))
	for k, v := range headers {
		headersNoCookies[strings.ToLower(k)] = v
	}
	return headersNoCookies
}

func normalizeHTTPHeaderName(name string) string {
	return strings.ToLower(name)
}

func normalizeHTTPHeaderValue(values []string) string {
	return strings.Join(values, ",")
}

func init() {
	makeCollectedHTTPHeadersLookupMap()
}

func makeCollectedHTTPHeadersLookupMap() {
	collectedHeadersLookupMap = make(map[string]struct{}, len(defaultCollectedHeaders)+len(clientip.MonitoredHeaders()))
	for _, h := range defaultCollectedHeaders {
		collectedHeadersLookupMap[h] = struct{}{}
	}
	// Whatever header the client IP resolver monitors is also collected as a
	// span tag. With the default policy these are already in the list above;
	// with DD_TRACE_CLIENT_IP_HEADER set, this is what adds the custom one.
	for _, h := range clientip.MonitoredHeaders() {
		collectedHeadersLookupMap[normalizeHTTPHeaderName(h)] = struct{}{}
	}
}

// setRequestHeadersTags sets the AppSec-specific request headers span tags.
func setRequestHeadersTags(span trace.TagSetter, headers map[string][]string) {
	setHeadersTags(span, "http.request.headers.", headers)
}

// setResponseHeadersTags sets the AppSec-specific response headers span tags.
func setResponseHeadersTags(span trace.TagSetter, headers map[string][]string) {
	setHeadersTags(span, "http.response.headers.", headers)
}

// setHeadersTags sets the AppSec-specific headers span tags.
func setHeadersTags(span trace.TagSetter, tagPrefix string, headers map[string][]string) {
	for h, v := range NormalizeHTTPHeaders(headers) {
		span.SetTag(tagPrefix+h, v)
	}
}
