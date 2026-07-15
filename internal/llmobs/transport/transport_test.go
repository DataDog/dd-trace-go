// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHasRetryAfterHeader(t *testing.T) {
	assert.False(t, hasRetryAfterHeader(http.Header{}))
	assert.True(t, hasRetryAfterHeader(http.Header{"Retry-After": []string{"5"}}))
	assert.True(t, hasRetryAfterHeader(http.Header{"X-Ratelimit-Reset": []string{"5"}}))
}

func TestParseRetryAfter(t *testing.T) {
	mk := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name string
		h    http.Header
		want time.Duration
	}{
		{"standard Retry-After (delta-seconds)", mk("Retry-After", "5"), 5 * time.Second},
		{"Retry-After wins over x-ratelimit-reset", mk("Retry-After", "7", "x-ratelimit-reset", "3"), 7 * time.Second},
		{"x-ratelimit-reset fallback (duration seconds)", mk("x-ratelimit-reset", "4"), 4 * time.Second},
		{"default 1s when no header", http.Header{}, time.Second},
		{"non-positive Retry-After falls back to default", mk("Retry-After", "0"), time.Second},
		{"unparseable Retry-After falls back to default", mk("Retry-After", "soon"), time.Second},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.h); got != tc.want {
			t.Errorf("%s: parseRetryAfter = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An HTTP-date in the future yields a positive, roughly-correct delay.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(mk("Retry-After", future)); got <= 0 || got > 31*time.Second {
		t.Errorf("HTTP-date Retry-After: got %v, want in (0, 31s]", got)
	}
}
