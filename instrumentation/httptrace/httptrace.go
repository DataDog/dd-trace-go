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
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	appsechttpsec "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/httpsec"
	listenerhttpsec "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	cfg = newConfig()
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageNetHTTP)
}

var reportTelemetryConfigOnce sync.Once

type inferredSpanCreatedCtxKey struct{}

type FinishSpanFunc = func(status int, errorFn func(int) bool, opts ...tracer.FinishOption)

// StartRequestSpan starts a server-side HTTP request span with the standard list of HTTP request span tags
// (http.method, http.url, http.useragent). Any further span start option can be added with opts.
func StartRequestSpan(r *http.Request, opts ...tracer.StartSpanOption) (*tracer.Span, context.Context, FinishSpanFunc) {
	// Append our span options before the given ones so that the caller can "overwrite" them.
	// TODO(): rework span start option handling (https://github.com/DataDog/dd-trace-go/issues/1352)

	// we cannot track the configuration in newConfig because it's called during init() and the the telemetry client
	// is not initialized yet
	reportTelemetryConfigOnce.Do(func() {
		telemetry.RegisterAppConfig("inferred_proxy_services_enabled", cfg.inferredProxyServicesEnabled, telemetry.OriginEnvVar)
		log.Debug("internal/httptrace: telemetry.RegisterAppConfig called with cfg: %s", cfg)
	})

	var ipTags map[string]string
	if cfg.traceClientIP {
		ipTags, _ = listenerhttpsec.ClientIPTags(r.Header, true, r.RemoteAddr)
	}

	var inferredProxySpan *tracer.Span

	if cfg.inferredProxyServicesEnabled {
		inferredProxySpanCreated := false

		if created, ok := r.Context().Value(inferredSpanCreatedCtxKey{}).(bool); ok {
			inferredProxySpanCreated = created
		}

		if !inferredProxySpanCreated {
			var inferredStartSpanOpts []tracer.StartSpanOption

			requestProxyContext, err := extractInferredProxyContext(r.Header)
			if err != nil {
				log.Debug("%s\n", err.Error())
			} else {
				// TODO: Baggage?
				spanParentCtx, spanParentErr := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
				if spanParentErr == nil {
					if spanParentCtx != nil && spanParentCtx.SpanLinks() != nil {
						inferredStartSpanOpts = append(inferredStartSpanOpts, tracer.WithSpanLinks(spanParentCtx.SpanLinks()))
					}
				}
				inferredProxySpan = startInferredProxySpan(requestProxyContext, spanParentCtx, inferredStartSpanOpts...)
			}
		}
	}

	parentCtx, extractErr := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
	if extractErr == nil && parentCtx != nil {
		ctx2 := r.Context()
		parentCtx.ForeachBaggageItem(func(k, v string) bool {
			ctx2 = baggage.Set(ctx2, k, v)
			return true
		})
		r = r.WithContext(ctx2)
	}

	nopts := make([]tracer.StartSpanOption, 0, len(opts)+1+len(ipTags))
	nopts = append(nopts,
		func(ssCfg *tracer.StartSpanConfig) {
			if ssCfg.Tags == nil {
				ssCfg.Tags = make(map[string]any)
			}
			ssCfg.Tags[ext.SpanType] = ext.SpanTypeWeb
			ssCfg.Tags[ext.HTTPMethod] = r.Method
			ssCfg.Tags[ext.HTTPURL] = URLFromRequest(r, cfg.queryString)
			ssCfg.Tags[ext.HTTPUserAgent] = r.UserAgent()
			ssCfg.Tags["_dd.measured"] = 1
			if r.Host != "" {
				ssCfg.Tags["http.host"] = r.Host
			}
			appsechttpsec.SetSecurityTestingHeaderTags(ssCfg.Tags, r.Header)

			if inferredProxySpan != nil {
				tracer.ChildOf(inferredProxySpan.Context())(ssCfg)
			} else if extractErr == nil && parentCtx != nil {
				if links := parentCtx.SpanLinks(); links != nil {
					tracer.WithSpanLinks(links)(ssCfg)
				}
				tracer.ChildOf(parentCtx)(ssCfg)
			}

			parentCtx.ForeachBaggageItem(func(k, v string) bool {
				if cfg.tagBaggageKey(k) {
					ssCfg.Tags["baggage."+k] = v
				}
				return true
			})

			for k, v := range ipTags {
				ssCfg.Tags[k] = v
			}
		})
	nopts = append(nopts, opts...)

	requestContext := r.Context()
	if inferredProxySpan != nil {
		requestContext = context.WithValue(requestContext, inferredSpanCreatedCtxKey{}, true)
	}

	span, ctx := tracer.StartSpanFromContext(requestContext, instr.OperationName(instrumentation.ComponentServer, nil), nopts...)
	return span, ctx, func(status int, errorFn func(int) bool, opts ...tracer.FinishOption) {
		FinishRequestSpan(span, status, errorFn, opts...)
		if inferredProxySpan != nil {
			FinishRequestSpan(inferredProxySpan, status, errorFn, opts...)
		}
	}
}

// FinishRequestSpan finishes the given HTTP request span and sets the expected response-related tags such as the status
// code. If not nil, errorFn will override the isStatusError method on httptrace for determining error codes. Any further span finish option can be added with opts.
func FinishRequestSpan(s *tracer.Span, status int, errorFn func(int) bool, opts ...tracer.FinishOption) {
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
			s.SetTag(ext.ErrorNoStackTrace, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
		} else {
			statusStr = "200"
		}
	} else {
		statusStr = strconv.Itoa(status)
		if fn(status) {
			s.SetTag(ext.ErrorNoStackTrace, fmt.Errorf("%s: %s", statusStr, http.StatusText(status)))
		}
	}
	fc := &tracer.FinishConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(fc)
	}
	if fc.NoDebugStack {
		// This is a workaround to ensure that the error stack is not set when NoDebugStack is true.
		// This is required because the error stack is set when we call `s.SetTag(ext.Error, err)` just
		// a few lines above.
		// This is also caused by the fact that the error stack generation is controlled by `tracer.WithDebugStack` (globally)
		// or `tracer.NoDebugStack` (per span, but only when we finish the span). These two options don't allow to control
		// the error stack generation per span that happens in `FinishRequestSpan` before calling `s.Finish`.
		s.SetTag("error.stack", "")
	}
	s.SetTag(ext.HTTPCode, statusStr)
	s.Finish(tracer.WithFinishConfig(fc))
}

// URLFromRequest returns the full URL from the HTTP request for server-side spans. If queryString is true, params are
// collected and obfuscated either by the default query string obfuscator or a custom one provided via
// DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP. When DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER is set it takes
// precedence and bypasses the obfuscator; otherwise DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST is used.
// See https://docs.datadoghq.com/tracing/configure_data_security/?tab=net#redact-query-strings for more information.
func URLFromRequest(r *http.Request, queryString bool) string {
	return urlFromRequest(r, queryString, false)
}

// URLFromClientRequest returns the full URL from the HTTP request for client-side spans. If queryString is true, params
// are collected and obfuscated either by the default query string obfuscator or a custom one provided via
// DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP. When DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT is set it takes
// precedence and bypasses the obfuscator; otherwise DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST is used.
// See https://docs.datadoghq.com/tracing/configure_data_security/?tab=net#redact-query-strings for more information.
func URLFromClientRequest(r *http.Request, queryString bool) string {
	return urlFromRequest(r, queryString, true)
}

func urlFromRequest(r *http.Request, queryString bool, isClient bool) string {
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
		allowlist := cfg.getQueryStringAllowlist(isClient)
		if allowlist != nil {
			// When an allowlist is configured, only keep the specified parameter keys.
			// This avoids running the expensive obfuscation regex entirely.
			query = filterQueryStringByAllowlist(query, allowlist)
		} else if cfg.useDefaultObfuscator {
			query = obfuscateQueryStringDefault(query)
		} else if cfg.queryStringRegexp != nil {
			query = cfg.queryStringRegexp.ReplaceAllLiteralString(query, "<redacted>")
		}
		if query != "" {
			url = strings.Join([]string{url, query}, "?")
		}
	}
	if frag := r.URL.EscapedFragment(); frag != "" {
		url = strings.Join([]string{url, frag}, "#")
	}
	return url
}

// filterQueryStringByAllowlist parses a raw query string and returns only the key=value
// pairs whose keys are in the allowlist. It operates on the raw string (splitting on & and =)
// to avoid unnecessary allocations from net/url.ParseQuery.
func filterQueryStringByAllowlist(rawQuery string, allowlist map[string]struct{}) string {
	var b strings.Builder
	for rawQuery != "" {
		// Split on "&" to isolate each key=value pair.
		var pair string
		if before, after, found := strings.Cut(rawQuery, "&"); found {
			pair, rawQuery = before, after
		} else {
			pair, rawQuery = rawQuery, ""
		}
		// Extract the key portion before "=" (or the whole pair if there's no value).
		key, _, _ := strings.Cut(pair, "=")
		if _, ok := allowlist[key]; ok {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(pair)
		}
	}
	return b.String()
}

// sensitiveKeywordsByFirstByte groups sensitive keywords by their lower-cased ASCII
// first byte. Within each bucket, order is preserved from the original flat list:
// longer keywords precede their prefixes (e.g. "api_key_id" before "api_key",
// "pass_phrase"/"passphrase" before "pass"). Changing bucket order is safe; changing
// intra-bucket order may break prefix disambiguation.
var sensitiveKeywordsByFirstByte = [5]struct {
	first    byte
	keywords []string
}{
	{'a', []string{
		"api_key_id", "api_keyid", "api_key", "apikey_id", "apikeyid", "apikey",
		"access_key_id", "access_keyid", "access_key", "accesskey_id", "accesskeyid", "accesskey",
		"authentication", "authorization", "auth",
	}},
	{'c', []string{
		"consumer_id", "consumer_key", "consumer_secret", "consumerid", "consumerkey", "consumersecret",
	}},
	{'p', []string{
		"password", "passwd", "pword", "pwd",
		"pass_phrase", "passphrase", "pass",
		"private_key_id", "private_keyid", "private_key", "privatekey_id", "privatekeyid", "privatekey",
		"public_key_id", "public_keyid", "public_key", "publickey_id", "publickeyid", "publickey",
	}},
	{'s', []string{
		"secret",
		"secret_key_id", "secret_keyid", "secret_key", "secretkey_id", "secretkeyid", "secretkey",
		"signed", "signature", "sign",
	}},
	{'t', []string{
		"token",
	}},
}

// Per-byte ASCII class bitmasks for the obfuscator's character classifiers.
// Each bit covers ALL characters that belong to that class (not just the extras).
// Non-ASCII bytes are handled by the Unicode fold fallback in each classifier.
const (
	classAlpha   uint8 = 1 << 0 // [a-zA-Z]
	classDigit   uint8 = 1 << 1 // [0-9]
	classWord    uint8 = 1 << 2 // [a-zA-Z0-9_]
	classBearer  uint8 = 1 << 3 // [a-zA-Z0-9._-]
	classSSHBody uint8 = 1 << 4 // [a-zA-Z0-9/+.]
	classJWTSeg  uint8 = 1 << 5 // [a-zA-Z0-9_=-]
	classJWTSig  uint8 = 1 << 6 // [a-zA-Z0-9_.+/=-]
)

// asciiClass is a 128-entry lookup table indexed by ASCII byte value.
// It collapses the per-byte range checks in the six character classifiers
// into a single load + bitmask test.
var asciiClass = func() [128]uint8 {
	var t [128]uint8
	// Alpha: [a-zA-Z] — member of all classes that include alpha.
	allAlpha := classAlpha | classWord | classBearer | classSSHBody | classJWTSeg | classJWTSig
	for c := byte('a'); c <= 'z'; c++ {
		t[c] |= allAlpha
		t[c-32] |= allAlpha // A-Z
	}
	// Digits: [0-9] — member of all classes that include digits.
	allDigit := classDigit | classWord | classBearer | classSSHBody | classJWTSeg | classJWTSig
	for c := byte('0'); c <= '9'; c++ {
		t[c] |= allDigit
	}
	// Extra single chars per class.
	t['_'] |= classWord | classBearer | classJWTSeg | classJWTSig
	t['.'] |= classBearer | classSSHBody | classJWTSig
	t['-'] |= classBearer | classJWTSeg | classJWTSig
	t['/'] |= classSSHBody | classJWTSig
	t['+'] |= classSSHBody | classJWTSig
	t['='] |= classJWTSeg | classJWTSig
	return t
}()

func emitObfuscated(b *strings.Builder, s string, last, pos, n int) int {
	if b.Len() == 0 {
		b.Grow(len(s))
	}
	b.WriteString(s[last:pos])
	b.WriteString("<redacted>")
	return pos + n
}

// obfuscateQueryStringDefault obfuscates s using the default query string
// obfuscation logic, equivalent to
// defaultQueryStringRegexp.ReplaceAllLiteralString(s, "<redacted>").
func obfuscateQueryStringDefault(s string) string {
	var b strings.Builder
	last := 0
	for pos := 0; pos < len(s); {
		if n, ok := matchDefaultObfuscatorSensitiveKey(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok := matchDefaultObfuscatorBearerToken(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok := matchDefaultObfuscatorShortToken(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok := matchDefaultObfuscatorGitHubToken(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok := matchDefaultObfuscatorJWT(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok := matchDefaultObfuscatorPEMPrivateKey(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
			continue
		}
		if n, ok, skip := matchDefaultObfuscatorSSHRSAKey(s, pos); ok {
			last = emitObfuscated(&b, s, last, pos, n)
			pos = last
		} else {
			pos += max(1, skip)
		}
	}
	if b.Len() == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func matchDefaultObfuscatorSensitiveKey(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c >= utf8.RuneSelf {
		// Non-ASCII: can fold to any keyword-initial letter; scan all buckets.
		// Cold path for RFC-3986 query strings.
		for i := range sensitiveKeywordsByFirstByte {
			for _, keyword := range sensitiveKeywordsByFirstByte[i].keywords {
				end, ok := matchFoldLiteral(s, pos, keyword)
				if !ok {
					continue
				}
				if suffixEnd, ok := matchDefaultObfuscatorSensitiveKeySuffix(s, end); ok {
					return suffixEnd - pos, true
				}
			}
		}
		return 0, false
	}
	var keywords []string
	switch toLowerASCII(c) {
	case 'a':
		keywords = sensitiveKeywordsByFirstByte[0].keywords
	case 'c':
		keywords = sensitiveKeywordsByFirstByte[1].keywords
	case 'p':
		keywords = sensitiveKeywordsByFirstByte[2].keywords
	case 's':
		keywords = sensitiveKeywordsByFirstByte[3].keywords
	case 't':
		keywords = sensitiveKeywordsByFirstByte[4].keywords
	default:
		return 0, false
	}
	for _, keyword := range keywords {
		end, ok := matchFoldLiteral(s, pos, keyword)
		if !ok {
			continue
		}
		if suffixEnd, ok := matchDefaultObfuscatorSensitiveKeySuffix(s, end); ok {
			return suffixEnd - pos, true
		}
	}
	return 0, false
}

func matchDefaultObfuscatorBearerToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "bearer"); !ok {
		return 0, false
	}
	spaceStart := pos
	pos = skipDefaultObfuscatorSpaces(s, pos)
	tokenEnd, ok := consumeDefaultObfuscatorBearerTokenChar(s, pos)
	if pos == spaceStart || !ok {
		return 0, false
	}
	// Quirk: the regexp has [a-z0-9._-] without a quantifier, so only one
	// token character after the spaces is redacted.
	return tokenEnd - start, true
}

func matchDefaultObfuscatorShortToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "token"); !ok {
		return 0, false
	}
	if pos < len(s) && s[pos] == ':' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3A"); !ok {
		return 0, false
	}
	for range 13 {
		if pos, ok = consumeDefaultObfuscatorAlphaNumChar(s, pos); !ok {
			return 0, false
		}
	}
	return pos - start, true
}

func matchDefaultObfuscatorGitHubToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "gh"); !ok {
		return 0, false
	}
	if pos, ok = consumeDefaultObfuscatorFoldedASCIISet(s, pos, "opsu"); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '_' {
		return 0, false
	}
	pos++
	for range 36 {
		if pos, ok = consumeDefaultObfuscatorAlphaNumChar(s, pos); !ok {
			return 0, false
		}
	}
	return pos - start, true
}

func matchDefaultObfuscatorJWT(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = consumeDefaultObfuscatorJWTHeader(s, pos); !ok {
		return 0, false
	}
	if pos, ok = consumeDefaultObfuscatorJWTSegment(s, pos); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '.' {
		return 0, false
	}
	pos++
	if pos, ok = consumeDefaultObfuscatorJWTHeader(s, pos); !ok {
		return 0, false
	}
	if pos, ok = consumeDefaultObfuscatorJWTSegment(s, pos); !ok {
		return 0, false
	}
	if pos < len(s) && s[pos] == '.' {
		if end, ok := consumeDefaultObfuscatorJWTSignature(s, pos+1); ok {
			pos = end
		}
	}
	return pos - start, true
}

func matchDefaultObfuscatorPEMPrivateKey(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchDefaultObfuscatorHyphens(s, pos, 5); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "BEGIN"); !ok {
		return 0, false
	}
	for _, labelEnd := range consumeDefaultObfuscatorPEMLabelEndPositions(s, pos) {
		afterKey, ok := matchDefaultObfuscatorPEMPrivateKeyLiteral(s, labelEnd)
		if !ok {
			continue
		}
		end, ok := matchDefaultObfuscatorPEMBodyAndEnd(s, afterKey)
		if ok {
			return end - start, true
		}
	}
	return 0, false
}

func matchDefaultObfuscatorPEMBodyAndEnd(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchDefaultObfuscatorHyphens(s, pos, 5); !ok {
		return 0, false
	}
	if pos, ok = consumeDefaultObfuscatorNonHyphenRun(s, pos); !ok {
		return 0, false
	}
	if pos, ok = matchDefaultObfuscatorHyphens(s, pos, 5); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "END"); !ok {
		return 0, false
	}
	return matchDefaultObfuscatorPEMFinalPrivateKey(s, pos)
}

func matchDefaultObfuscatorPEMFinalPrivateKey(s string, pos int) (int, bool) {
	for _, labelEnd := range consumeDefaultObfuscatorPEMLabelEndPositions(s, pos) {
		if end, ok := matchDefaultObfuscatorPEMPrivateKeyLiteral(s, labelEnd); ok {
			return end, true
		}
	}
	return 0, false
}

func matchDefaultObfuscatorPEMPrivateKeyLiteral(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "PRIVATE"); !ok {
		return 0, false
	}
	if pos, ok = consumeDefaultObfuscatorSpaceOrPct20(s, pos); !ok {
		return 0, false
	}
	return matchFoldLiteral(s, pos, "KEY")
}

// matchDefaultObfuscatorSSHRSAKey returns (matchedLen, ok, safeSkip).
// On failure (ok=false), safeSkip is the number of bytes from pos that the
// outer loop can safely skip without missing any other match. This avoids
// re-scanning the entire key body when the key is too short.
//
// Safe-skip correctness table (matchers vs. SSH-RSA body charset [a-zA-Z0-9/+.]):
//   sensitive key (p/a/s/c/t)  — letters present, but suffix needs '='/'%3D'/'"'/':', none in body → safe
//   bearer                     — needs space after "bearer"; space not in body → safe
//   short-token                — needs ':' after "token"; ':' not in body → safe
//   github                     — needs '_' after gh[opsu]; '_' not in body → safe
//   JWT (eyJ…)                 — '.' in body charset; 'e'/'E' can start JWT → NOT safe; stop skip at e/E
//   PEM (-----)                — '-' not in body → safe
//   SSH-RSA itself             — '-' not in body → cannot re-anchor → safe
func matchDefaultObfuscatorSSHRSAKey(s string, pos int) (matchedLen int, ok bool, safeSkip int) {
	start := pos
	var matched bool
	if pos, matched = matchFoldLiteral(s, pos, "ssh-rsa"); !matched {
		return 0, false, 0
	}
	pos = skipDefaultObfuscatorSpaces(s, pos)
	// safeEnd tracks the furthest position that is safe to skip to on failure.
	// We advance it for each single-byte body char that is not 'e'/'E'
	// (which could anchor a JWT match).
	safeEnd := pos
	count := 0
	for {
		next, ok := consumeDefaultObfuscatorSSHRSAKeyChar(s, pos)
		if !ok {
			break
		}
		// Only extend safeEnd for single-byte advances that aren't 'e'/'E'.
		// Percent-encoded chars (next=pos+3) are fine to skip but we keep
		// safeEnd conservative by only advancing on single-byte steps.
		if next == pos+1 && s[pos]|32 != 'e' {
			safeEnd = next
		}
		pos = next
		count++
	}
	if count < 100 {
		return 0, false, safeEnd - start
	}
	return pos - start, true, 0
}

func matchDefaultObfuscatorSensitiveKeySuffix(s string, pos int) (int, bool) {
	if end, ok := matchDefaultObfuscatorSensitiveKeyValue(s, pos); ok {
		return end, true
	}
	return matchDefaultObfuscatorSensitiveKeyJSON(s, pos)
}

func matchDefaultObfuscatorSensitiveKeyValue(s string, pos int) (int, bool) {
	pos = skipDefaultObfuscatorSpaces(s, pos)
	var ok bool
	if pos < len(s) && s[pos] == '=' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3D"); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] == '&' {
		return 0, false
	}
	for pos < len(s) && s[pos] != '&' {
		pos++
	}
	return pos, true
}

func matchDefaultObfuscatorSensitiveKeyJSON(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchDefaultObfuscatorQuote(s, pos); !ok {
		return 0, false
	}
	pos = skipDefaultObfuscatorSpaces(s, pos)
	if pos < len(s) && s[pos] == ':' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3A"); !ok {
		return 0, false
	}
	pos = skipDefaultObfuscatorSpaces(s, pos)
	if pos, ok = matchDefaultObfuscatorQuote(s, pos); !ok {
		return 0, false
	}
	valueStart := pos
	pos = consumeDefaultObfuscatorJSONValue(s, pos)
	if pos == valueStart {
		return 0, false
	}
	if pos, ok = matchDefaultObfuscatorQuote(s, pos); !ok {
		return 0, false
	}
	return pos, true
}

func matchDefaultObfuscatorQuote(s string, pos int) (int, bool) {
	if pos < len(s) && s[pos] == '"' {
		return pos + 1, true
	}
	return matchFoldLiteral(s, pos, "%22")
}

func skipDefaultObfuscatorSpaces(s string, pos int) int {
	for pos < len(s) {
		if isDefaultObfuscatorSpace(s[pos]) {
			pos++
			continue
		}
		if next, ok := matchFoldLiteral(s, pos, "%20"); ok {
			pos = next
			continue
		}
		return pos
	}
	return pos
}

func matchDefaultObfuscatorHyphens(s string, pos int, n int) (int, bool) {
	if len(s)-pos < n {
		return 0, false
	}
	for i := range n {
		if s[pos+i] != '-' {
			return 0, false
		}
	}
	return pos + n, true
}

func isDefaultObfuscatorSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	default:
		return false
	}
}

func consumeDefaultObfuscatorPEMLabelEndPositions(s string, pos int) []int {
	var positions []int
	for {
		next, ok := consumeDefaultObfuscatorPEMLabelChar(s, pos)
		if !ok {
			break
		}
		pos = next
		positions = append(positions, pos)
	}
	slices.Reverse(positions)
	return positions
}

func consumeDefaultObfuscatorPEMLabelChar(s string, pos int) (int, bool) {
	if next, ok := consumeDefaultObfuscatorAlphaChar(s, pos); ok {
		return next, true
	}
	if next, ok := consumeDefaultObfuscatorSpaceOrPct20(s, pos); ok {
		return next, true
	}
	return 0, false
}

func consumeDefaultObfuscatorSpaceOrPct20(s string, pos int) (int, bool) {
	if pos < len(s) && isDefaultObfuscatorSpace(s[pos]) {
		return pos + 1, true
	}
	return matchFoldLiteral(s, pos, "%20")
}

func consumeDefaultObfuscatorNonHyphenRun(s string, pos int) (int, bool) {
	i := strings.IndexByte(s[pos:], '-')
	if i == 0 {
		return 0, false
	}
	if i < 0 {
		return len(s), true
	}
	return pos + i, true
}

func consumeDefaultObfuscatorJWTHeader(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "ey"); !ok {
		return 0, false
	}
	return consumeDefaultObfuscatorFoldedASCIISet(s, pos, "ijkl")
}

func consumeDefaultObfuscatorJWTSegment(s string, pos int) (int, bool) {
	start := pos
	for {
		next, ok := consumeDefaultObfuscatorJWTSegmentChar(s, pos)
		if !ok {
			break
		}
		pos = next
	}
	if pos == start {
		return 0, false
	}
	return pos, true
}

func consumeDefaultObfuscatorJWTSegmentChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classJWTSeg != 0 {
			return pos + 1, true
		}
		if c == '%' {
			return matchFoldLiteral(s, pos, "%3D")
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorJWTSignature(s string, pos int) (int, bool) {
	start := pos
	for {
		next, ok := consumeDefaultObfuscatorJWTSignatureChar(s, pos)
		if !ok {
			break
		}
		pos = next
	}
	if pos == start {
		return 0, false
	}
	return pos, true
}

func consumeDefaultObfuscatorJWTSignatureChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classJWTSig != 0 {
			return pos + 1, true
		}
		if c == '%' {
			if next, ok := matchFoldLiteral(s, pos, "%3D"); ok {
				return next, true
			}
			if next, ok := matchFoldLiteral(s, pos, "%2F"); ok {
				return next, true
			}
			return matchFoldLiteral(s, pos, "%2B")
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorBearerTokenChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classBearer != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorSSHRSAKeyChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classSSHBody != 0 {
			return pos + 1, true
		}
		if c == '%' {
			if next, ok := matchFoldLiteral(s, pos, "%2F"); ok {
				return next, true
			}
			if next, ok := matchFoldLiteral(s, pos, "%5C"); ok {
				return next, true
			}
			return matchFoldLiteral(s, pos, "%2B")
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}


func consumeDefaultObfuscatorAlphaChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classAlpha != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorAlphaNumChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&(classAlpha|classDigit) != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorFoldedASCIISet(s string, pos int, chars string) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	if s[pos] < utf8.RuneSelf {
		if strings.IndexByte(chars, toLowerASCII(s[pos])) >= 0 {
			return pos + 1, true
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if strings.IndexByte(chars, byte(folded)) >= 0 {
			return pos + width, true
		}
	}
	return 0, false
}

func consumeDefaultObfuscatorJSONValue(s string, pos int) int {
	for pos < len(s) {
		// The value regexp is (%2[^2]|%[^2]|[^"%])+. The order is
		// observable for inputs such as %20 and %2", so keep it verbatim.
		if next, ok := consumeDefaultObfuscatorJSONValuePct2(s, pos); ok {
			pos = next
			continue
		}
		if next, ok := consumeDefaultObfuscatorJSONValuePct(s, pos); ok {
			pos = next
			continue
		}
		r, width := utf8.DecodeRuneInString(s[pos:])
		if r == '"' || r == '%' {
			return pos
		}
		pos += width
	}
	return pos
}

func consumeDefaultObfuscatorJSONValuePct2(s string, pos int) (int, bool) {
	next, ok := matchFoldLiteral(s, pos, "%2")
	if !ok || next >= len(s) {
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[next:])
	if r == '2' {
		return 0, false
	}
	return next + width, true
}

func consumeDefaultObfuscatorJSONValuePct(s string, pos int) (int, bool) {
	if pos >= len(s) || s[pos] != '%' || pos+1 >= len(s) {
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos+1:])
	if r == '2' {
		return 0, false
	}
	return pos + 1 + width, true
}

func matchFoldLiteral(s string, pos int, lit string) (int, bool) {
	for i := 0; i < len(lit); i++ {
		if pos >= len(s) {
			return 0, false
		}
		want := lit[i]
		if isASCIILetter(want) {
			if s[pos] < utf8.RuneSelf {
				if toLowerASCII(s[pos]) != toLowerASCII(want) {
					return 0, false
				}
				pos++
				continue
			}
			r, width := utf8.DecodeRuneInString(s[pos:])
			if !equalFoldASCII(r, toLowerASCII(want)) {
				return 0, false
			}
			pos += width
			continue
		}
		if s[pos] != want {
			return 0, false
		}
		pos++
	}
	return pos, true
}

func equalFoldASCII(r rune, lower byte) bool {
	want := rune(lower)
	for folded := r; ; folded = unicode.SimpleFold(folded) {
		if folded == want {
			return true
		}
		next := unicode.SimpleFold(folded)
		if next == r {
			return false
		}
	}
}

func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func toLowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// HeaderTagsFromRequest matches req headers to user-defined list of header tags
// and creates span tags based on the header tag target and the req header value
func HeaderTagsFromRequest(req *http.Request, headerTags instrumentation.HeaderTags) tracer.StartSpanOption {
	var tags []struct {
		key string
		val string
	}

	headerTags.Iter(func(header, tag string) {
		if vs, ok := req.Header[header]; ok {
			tags = append(tags, struct {
				key string
				val string
			}{tag, strings.TrimSpace(strings.Join(vs, ","))})
		}
	})

	return func(cfg *tracer.StartSpanConfig) {
		for _, t := range tags {
			cfg.Tags[t.key] = t.val
		}
	}
}
