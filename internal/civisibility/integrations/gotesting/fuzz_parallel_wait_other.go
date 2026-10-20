//go:build !windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"
	"time"
)

// Outside Windows, testing's highPrecisionTime is time.Time, so the wait that
// completeParallelSubtestsWithState measures with time.Since and fRunner's
// duration accumulator share one clock. The subtraction needs no skew guard.
func capFuzzParallelSeedsWait(_ *testing.F, wait time.Duration) time.Duration {
	return wait
}
