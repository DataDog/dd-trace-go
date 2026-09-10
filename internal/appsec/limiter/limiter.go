// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022-present Datadog, Inc.

// Package limiter provides simple rate limiting primitives backed by a synchronous token bucket.
package limiter

import (
	"math"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is used to abstract the rate limiter implementation to only expose the needed function for rate limiting.
// This is for example useful for testing, allowing us to use a modified rate limiter tuned for testing through the same
// interface.
type Limiter interface {
	Allow() bool
}

// NewTokenTicker is a utility function that allocates a token ticker, initializes necessary fields and returns it
func NewTokenTicker(tokens, maxTokens int64) Limiter {
	return newLimiter(tokens, maxTokens, time.Second)
}

// NewTokenTickerWithInterval is a utility function that allocates a token ticker with a custom interval
func NewTokenTickerWithInterval(tokens, maxTokens int64, interval time.Duration) Limiter {
	return newLimiter(tokens, maxTokens, interval)
}

func newLimiter(tokens, maxTokens int64, interval time.Duration) *rate.Limiter {
	tokens = max(0, min(tokens, maxTokens))
	if maxTokens <= 0 || interval <= 0 {
		// Non-positive intervals are deny-safe: computing a rate from them could create an infinite limiter.
		return rate.NewLimiter(0, 0)
	}

	burst := int(maxTokens)
	if maxTokens > int64(math.MaxInt) {
		burst = math.MaxInt
	}

	limit := rate.Limit(float64(maxTokens) / interval.Seconds())
	lim := rate.NewLimiter(limit, burst)
	t0 := time.Now()
	initial := min(tokens, int64(burst))
	if drain := int64(burst) - initial; drain > 0 {
		lim.AllowN(t0, int(drain))
	}

	return lim
}
