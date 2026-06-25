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

// TokenTicker is a thread-safe rate limiter based on a lazily refilled token bucket.
// Start and Stop are retained for compatibility and do not affect refill behavior.
type TokenTicker struct {
	lim *rate.Limiter
	now func() time.Time
}

// NewTokenTicker is a utility function that allocates a token ticker, initializes necessary fields and returns it
func NewTokenTicker(tokens, maxTokens int64) *TokenTicker {
	return newTokenTicker(tokens, maxTokens, time.Second, time.Now)
}

// NewTokenTickerWithInterval is a utility function that allocates a token ticker with a custom interval
func NewTokenTickerWithInterval(tokens, maxTokens int64, interval time.Duration) *TokenTicker {
	return newTokenTicker(tokens, maxTokens, interval, time.Now)
}

func newTokenTicker(tokens, maxTokens int64, interval time.Duration, now func() time.Time) *TokenTicker {
	if now == nil {
		now = time.Now
	}

	if tokens < 0 {
		tokens = 0
	} else if tokens > maxTokens {
		tokens = maxTokens
	}

	if maxTokens <= 0 || interval <= 0 {
		// Non-positive intervals are deny-safe: computing a rate from them could create an infinite limiter.
		return &TokenTicker{lim: rate.NewLimiter(0, 0), now: now}
	}

	burst := int(maxTokens)
	if maxTokens > int64(math.MaxInt) {
		burst = math.MaxInt
	}

	limit := rate.Limit(float64(maxTokens) / interval.Seconds())
	lim := rate.NewLimiter(limit, burst)
	t0 := now()
	initial := tokens
	if initial > int64(burst) {
		initial = int64(burst)
	}
	if drain := int64(burst) - initial; drain > 0 {
		lim.AllowN(t0, int(drain))
	}

	return &TokenTicker{lim: lim, now: now}
}

// Start is retained for compatibility and is safe to call any number of times.
func (t *TokenTicker) Start() {}

// Stop is retained for compatibility and is safe to call any number of times.
func (t *TokenTicker) Stop() {
}

// Allow checks and returns whether a token can be retrieved from the bucket and consumed.
// Thread-safe.
func (t *TokenTicker) Allow() bool {
	// Use AllowN with the injected clock; Allow reads time.Now internally and would bypass tests.
	return t.lim.AllowN(t.now(), 1)
}
