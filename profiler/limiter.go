// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package profiler

import (
	"time"
)

// rateLimiter limits the number of actions that can be taken in
// a time period, which can be conditionally activated
type rateLimiter struct {
	active     bool
	tokens     int
	capacity   int
	period     time.Duration
	activation time.Time
}

// newRateLimiter returns a limiter which allows up to count actions
// to be taken in the given time period, once activated
func newRateLimiter(count int, period time.Duration) *rateLimiter {
	return &rateLimiter{
		capacity: count,
		period:   period,
	}
}

// activate activates the rate limiter if it is not already active
func (l *rateLimiter) activate() {
	if l.active {
		return
	}
	l.activation = time.Now()
	l.tokens = l.capacity
	l.active = true
}

// allow requests to take an action and returns whether the action can be taken
// given the configured limit, if the limiter is active
func (l *rateLimiter) allow() bool {
	if !l.active {
		return true
	}
	if time.Since(l.activation) > l.period {
		l.active = false
		return true
	}
	l.tokens--
	return l.tokens >= 0
}
