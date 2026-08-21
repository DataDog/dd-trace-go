// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryDelay(t *testing.T) {
	for name, tt := range map[string]struct {
		pollInterval time.Duration
		attempt      int
		random       float64
		wantMin      time.Duration
		wantMax      time.Duration
	}{
		"30s attempt 1, random 0.0":             {30 * time.Second, 1, 0.0, 4 * time.Second, 4 * time.Second},
		"30s attempt 1, random 1.0":             {30 * time.Second, 1, 1.0, 6 * time.Second, 6 * time.Second},
		"30s attempt 1, random 0.5":             {30 * time.Second, 1, 0.5, 4 * time.Second, 6 * time.Second},
		"30s attempt 2, random 0.0":             {30 * time.Second, 2, 0.0, 8 * time.Second, 8 * time.Second},
		"30s attempt 2, random 1.0":             {30 * time.Second, 2, 1.0, 12 * time.Second, 12 * time.Second},
		"1s attempt 1 clamps to 2s floor":       {1 * time.Second, 1, 0.0, 1600 * time.Millisecond, 1600 * time.Millisecond},
		"1s attempt 2 clamps to 5s floor":       {1 * time.Second, 2, 0.0, 4 * time.Second, 4 * time.Second},
		"3600s attempt 1 clamps to 10s ceiling": {3600 * time.Second, 1, 1.0, 12 * time.Second, 12 * time.Second},
		"3600s attempt 2 clamps to 30s ceiling": {3600 * time.Second, 2, 1.0, 36 * time.Second, 36 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			delay := retryDelay(tt.pollInterval, tt.attempt, tt.random)
			assert.GreaterOrEqual(t, delay, tt.wantMin)
			assert.LessOrEqual(t, delay, tt.wantMax)
		})
	}
}

func TestIsRetryablePollStatus(t *testing.T) {
	for status, want := range map[int]bool{
		0:   true,
		200: false,
		204: false,
		304: false,
		400: false,
		401: false,
		403: false,
		404: false,
		408: true,
		429: true,
		499: false,
		500: true,
		503: true,
		599: true,
	} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			assert.Equal(t, want, isRetryablePollStatus(status), "status %d", status)
		})
	}
}

func TestRetryAfterDuration(t *testing.T) {
	newHeader := func(name string, value string) http.Header {
		h := http.Header{}
		if name != "" {
			h.Set(name, value)
		}
		return h
	}

	for name, tt := range map[string]struct {
		header string
		value  string
		want   time.Duration
	}{
		"absent":            {"", "", 0},
		"delta seconds":     {"Retry-After", "5", 5 * time.Second},
		"zero is ignored":   {"Retry-After", "0", 0},
		"negative ignored":  {"Retry-After", "-5", 0},
		"garbage ignored":   {"Retry-After", "not-a-value", 0},
		"past date ignored": {"Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	} {
		t.Run(name, func(t *testing.T) {
			got := retryAfterDuration(newHeader(tt.header, tt.value))
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("future HTTP-date", func(t *testing.T) {
		future := time.Now().Add(30 * time.Second).UTC()
		got := retryAfterDuration(newHeader("Retry-After", future.Format(http.TimeFormat)))
		assert.InDelta(t, 30*time.Second, got, float64(2*time.Second))
	})
}
