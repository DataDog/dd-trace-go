// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httpsec

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClientIdentity covers the emitter's half of the resolve-once contract. The
// request below is built so the default policy would answer differently from the
// supplied identity, which is what makes each case tell them apart: a supplied
// identity must survive untouched, and a caller that supplies none must still get
// the default policy applied on its behalf.
func TestClientIdentity(t *testing.T) {
	trusted := netip.MustParseAddr("82.67.164.163")

	for name, tc := range map[string]struct {
		config       Config
		wantRemoteIP string
		wantClientIP string
	}{
		"supplied identity is used verbatim": {
			config:       Config{RemoteIP: trusted, ClientIP: trusted},
			wantRemoteIP: "82.67.164.163",
			wantClientIP: "82.67.164.163",
		},
		"a supplied client IP alone is enough": {
			config:       Config{ClientIP: trusted},
			wantRemoteIP: "invalid IP",
			wantClientIP: "82.67.164.163",
		},
		"no supplied identity falls back to the default policy": {
			config:       Config{},
			wantRemoteIP: "10.0.0.1",
			wantClientIP: "203.0.113.77",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
			// A forged public entry the default policy would pick.
			r.Header.Set("X-Forwarded-For", "203.0.113.77")
			r.RemoteAddr = "10.0.0.1:4242"

			cfg := tc.config
			remoteIP, clientIP := clientIdentity(&cfg, r)

			assert.Equal(t, tc.wantRemoteIP, remoteIP.String())
			assert.Equal(t, tc.wantClientIP, clientIP.String())
		})
	}
}
