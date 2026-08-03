// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
)

// countingResolver wraps the default policy and records how many times a request
// caused it to run.
func countingResolver(t *testing.T) *int {
	t.Helper()

	var calls int
	old := resolveClientIP
	resolveClientIP = func(hdrs map[string][]string, hasCanonicalHeaders bool, remoteAddr string) (netip.Addr, netip.Addr) {
		calls++
		return old(hdrs, hasCanonicalHeaders, remoteAddr)
	}
	t.Cleanup(func() { resolveClientIP = old })

	return &calls
}

// TestResolverInvokedOnce is the reason the resolved identity crosses the
// instrumentation boundary as a value: a request must decide who the client is
// exactly once, rather than having each consumer scan the headers again and
// risk reaching a different answer.
//
// This counts the resolutions this package performs. That the AppSec emitter
// does not add one of its own on top is covered by TestClientIdentity in
// instrumentation/appsec/emitter/httpsec.
func TestResolverInvokedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	mt := mocktracer.Start()
	defer mt.Stop()

	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec is not available in this build")
	}
	ResetCfg()

	calls := countingResolver(t)

	r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.77")
	r.RemoteAddr = "82.67.164.163:4242"
	w := httptest.NewRecorder()

	rw, rt, after, handled := BeforeHandle(&ServeConfig{Route: "/test"}, w, r)
	require.False(t, handled)
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
	after()

	assert.Equal(t, 1, *calls, "the client IP must be resolved exactly once per request")

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "203.0.113.77", spans[0].Tag(ext.HTTPClientIP))
	assert.Equal(t, "82.67.164.163", spans[0].Tag(ext.NetworkClientIP))
}

// TestClientIPOverrideSkipsResolver covers the channel an integration uses when
// its infrastructure already told it the trustworthy address: the default policy
// must not run at all, and the address it would have produced must not appear.
func TestClientIPOverrideSkipsResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	mt := mocktracer.Start()
	defer mt.Stop()

	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec is not available in this build")
	}
	ResetCfg()

	calls := countingResolver(t)

	trusted := netip.MustParseAddr("82.67.164.163")
	r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
	// A forged public entry that the default policy would pick.
	r.Header.Set("X-Forwarded-For", "203.0.113.77")
	w := httptest.NewRecorder()

	rw, rt, after, handled := BeforeHandle(&ServeConfig{
		Route:    "/test",
		RemoteIP: trusted,
		ClientIP: trusted,
	}, w, r)
	require.False(t, handled)
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
	after()

	assert.Zero(t, *calls, "an integration-supplied identity must not trigger the default resolver")

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "82.67.164.163", spans[0].Tag(ext.HTTPClientIP))
	assert.Equal(t, "82.67.164.163", spans[0].Tag(ext.NetworkClientIP))
	// The forged header must not have decided anything...
	assert.NotEqual(t, "203.0.113.77", spans[0].Tag(ext.HTTPClientIP))
	// ...but it must still be visible to the WAF and the trace, verbatim.
	assert.Equal(t, "203.0.113.77", spans[0].Tag("http.request.headers.x-forwarded-for"))
}

// TestClientIPInvariant asserts the property the whole boundary change exists
// for: the identity on the span and the identity AppSec evaluates are the same
// value, whether it came from the default resolver or from an integration.
func TestClientIPInvariant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	t.Setenv("DD_APPSEC_ENABLED", "true")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec is not available in this build")
	}
	ResetCfg()

	trusted := netip.MustParseAddr("82.67.164.163")

	for name, tc := range map[string]struct {
		serveConfig ServeConfig
		headers     map[string]string
		remoteAddr  string
		wantIP      string
	}{
		"resolved from headers": {
			serveConfig: ServeConfig{Route: "/test"},
			headers:     map[string]string{"X-Forwarded-For": "203.0.113.77"},
			remoteAddr:  "10.0.0.1:4242",
			wantIP:      "203.0.113.77",
		},
		"resolved from the remote address": {
			serveConfig: ServeConfig{Route: "/test"},
			remoteAddr:  "82.67.164.163:4242",
			wantIP:      "82.67.164.163",
		},
		"supplied by the integration": {
			serveConfig: ServeConfig{Route: "/test", RemoteIP: trusted, ClientIP: trusted},
			headers:     map[string]string{"X-Forwarded-For": "203.0.113.77, 82.67.164.163, 8.233.57.190"},
			remoteAddr:  "10.0.0.1:4242",
			wantIP:      "82.67.164.163",
		},
	} {
		t.Run(name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if tc.remoteAddr != "" {
				r.RemoteAddr = tc.remoteAddr
			}
			w := httptest.NewRecorder()

			serveCfg := tc.serveConfig
			rw, rt, after, handled := BeforeHandle(&serveCfg, w, r)
			require.False(t, handled)
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
			after()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			// The span tag is written twice — once at span construction and once
			// by the AppSec listener — and both writes read the same resolved
			// pair, so agreeing on this value is what proves they cannot drift.
			assert.Equal(t, tc.wantIP, spans[0].Tag(ext.HTTPClientIP))
		})
	}
}

// TestStartRequestSpanResolvesStandalone pins that the public entry point keeps
// resolving on its own for callers that never reach BeforeHandle.
func TestStartRequestSpanResolvesStandalone(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	mt := mocktracer.Start()
	defer mt.Stop()

	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	ResetCfg()

	calls := countingResolver(t)

	r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.77")
	s, _, _ := StartRequestSpan(r)
	s.Finish()

	assert.Equal(t, 1, *calls)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "203.0.113.77", spans[0].Tag(ext.HTTPClientIP))
}
