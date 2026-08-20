// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
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
