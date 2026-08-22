//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math"
	"testing"
	"time"
)

func TestParallelCounterDuration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		delta     int64
		frequency int64
		want      time.Duration
		ok        bool
	}{
		{name: "exact", delta: 10, frequency: 10, want: time.Second, ok: true},
		{name: "fractional", delta: 1, frequency: 3, want: 333333333 * time.Nanosecond, ok: true},
		{name: "negative delta", delta: -1, frequency: 1},
		{name: "zero frequency", delta: 1, frequency: 0},
		{name: "overflow", delta: math.MaxInt64, frequency: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parallelCounterDuration(tc.delta, tc.frequency)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parallelCounterDuration(%d, %d) = (%v, %t), want (%v, %t)", tc.delta, tc.frequency, got, ok, tc.want, tc.ok)
			}
		})
	}
}
