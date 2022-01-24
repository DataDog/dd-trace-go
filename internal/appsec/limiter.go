// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"sync/atomic"
	"time"
)

type Limiter interface {
	Allow() bool
}

// TokenTicker is a thread-safe and lock-free rate limiter based on a token bucket.
// The idea is to have a goroutine that will update  the bucket with fresh tokens at regular intervals using a time.Ticker.
// The advantage of using a goroutine here is  that the implementation becomes easily thread-safe using a few
// atomic operations with little overhead overall. TokenTicker.Start() *should* be called before the first call to
// TokenTicker.Allow() and TokenTicker.Stop() *must* be called once done using. Note that calling TokenTicker.Allow()
// before TokenTicker.Start() is valid, but it means the bucket won't be refilling until the call to TokenTicker.Start() is made
type TokenTicker struct {
	tokens    int64
	maxTokens int64
	ticker    *time.Ticker
	stopChan  chan struct{}
}

func NewTokenTicker(tokens, maxTokens int64) *TokenTicker {
	return &TokenTicker{
		tokens:    tokens,
		maxTokens: maxTokens,
		stopChan:  make(chan struct{}),
	}
}

// updateBucket performs a select loop to update the token amount in the bucket.
// Used in a goroutine by the rate limiter.
func (t *TokenTicker) updateBucket(startTime time.Time) {
	nsPerToken := time.Second.Nanoseconds() / t.maxTokens
	elapsedNs := int64(0)
	prevStamp := startTime

	for {
		select {
		case <-t.stopChan:
			return
		case stamp := <-t.ticker.C:
			// Compute the time in nanoseconds that passed between the previous timestamp and this one
			// This will be used to know how many tokens can be added into the bucket depending on the limiter rate
			elapsedNs += stamp.Sub(prevStamp).Nanoseconds()
			if elapsedNs > t.maxTokens*nsPerToken {
				elapsedNs = t.maxTokens * nsPerToken
			}
			prevStamp = stamp
			// Update the number of tokens in the bucket if enough nanoseconds have passed
			if elapsedNs >= nsPerToken {
				// Atomic spin lock to make sure we don't race for `t.tokens`
				for {
					tokens := atomic.LoadInt64(&t.tokens)
					if tokens == t.maxTokens {
						break // Bucket is already full, nothing to do
					}
					inc := elapsedNs / nsPerToken
					// Make sure not to add more tokens than we are allowed to into the bucket
					if tokens+inc > t.maxTokens {
						inc -= (tokens + inc) % t.maxTokens
					}
					if atomic.CompareAndSwapInt64(&t.tokens, tokens, tokens+inc) {
						// Keep track of remaining elapsed ns that were not taken into account for this computation,
						//so that increment computation remains precise over time
						elapsedNs = elapsedNs % nsPerToken
						break
					}
				}
			}
		}
	}
}

func (t *TokenTicker) Start() {
	timeNow := time.Now()
	t.ticker = time.NewTicker(500 * time.Microsecond)
	//Ticker goroutine: ticks every 500ms to check whether tokens can be added to the bucket or not
	go t.updateBucket(timeNow)
}

// start() is used for internal testing. Controlling the ticker means being able to test per-tick
// rather than per-duration, which is more reliable if the app is under a lot of stress
func (t *TokenTicker) start(ticksChan chan time.Time, startTime time.Time) {
	t.ticker = &time.Ticker{C: ticksChan}
	// Ticker goroutine: ticks every 500ms to check whether tokens can be added to the bucket or not
	go t.updateBucket(startTime)
}

func (t *TokenTicker) Stop() {
	t.ticker.Stop()
	close(t.stopChan)
}

func (t *TokenTicker) Allow() bool {
	for {
		tokens := atomic.LoadInt64(&t.tokens)
		if tokens == 0 {
			return false
		} else if atomic.CompareAndSwapInt64(&t.tokens, tokens, tokens-1) {
			return true
		}
	}
}
