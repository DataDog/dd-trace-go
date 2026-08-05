// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/clientip"
)

func TestClientIdentity(t *testing.T) {
	trusted := netip.MustParseAddr("82.67.164.163")
	for name, tc := range map[string]struct {
		config Config
		want   string
	}{
		"supplied": {Config{ClientIP: trusted}, "82.67.164.163"},
		"resolved": {Config{}, "203.0.113.77"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
			r.Header.Set("X-Forwarded-For", "203.0.113.77")
			assert.Equal(t, tc.want, clientIdentity(&tc.config, r).String())
		})
	}
}

func TestClientIdentityCustomHeaderPrecedence(t *testing.T) {
	t.Cleanup(clientip.ResetConfig)
	t.Setenv("DD_TRACE_CLIENT_IP_HEADER", "CF-Connecting-IP")
	clientip.ResetConfig()
	r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.Header.Set("CF-Connecting-IP", "82.67.164.163")
	cfg := Config{ClientIP: netip.MustParseAddr("8.233.57.190")}
	assert.Equal(t, "82.67.164.163", clientIdentity(&cfg, r).String())
}
