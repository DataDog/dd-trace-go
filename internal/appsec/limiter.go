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

//The token ticker is a thread-safe and lock-free rate limiter based on a token bucket.
//The idea is to have a goroutine that will update  the bucket with fresh tokens at regular intervals using a time.Ticker.
//The advantage of using a goroutine here is  that the implementation becomes easily thread-safe using a few
//atomic operations with little overhead overall. TokenTicker.Start() *must* be called before the first call to
//TokenTicker.Allow() and TokenTicker.Stop() *must* be called once done using.
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

//Select loop to update the token amount in the bucket. Used in a goroutine by the rate limiter.
func (t *TokenTicker) updateBucket(startTime time.Time) {
	ticksPerToken := (time.Second.Nanoseconds() / t.maxTokens)
	ticks := int64(0)
	prevStamp := startTime

	for {
		select {
		case <-t.stopChan:
			return
		case stamp := <-t.ticker.C:
			ticks += stamp.Sub(prevStamp).Nanoseconds()
			if ticks > t.maxTokens*ticksPerToken {
				ticks = t.maxTokens * ticksPerToken
			}
			prevStamp = stamp
			if ticks >= ticksPerToken {
				for {
					tokens := atomic.LoadInt64(&t.tokens)
					if tokens == t.maxTokens {
						break
					}
					inc := ticks / ticksPerToken
					if tokens+inc > t.maxTokens {
						inc -= (tokens + inc) % t.maxTokens
					}
					if atomic.CompareAndSwapInt64(&t.tokens, tokens, tokens+inc) {
						ticks = ticks % ticksPerToken
						break
					}
				}
			}
		}
	}
}

func (t *TokenTicker) Start() {
	if t.ticker != nil {
		t.Stop()
	}

	t.ticker = time.NewTicker(500 * time.Microsecond)
	//Ticker goroutine: ticks every 500ms to check whether tokens can be added to the bucket or not
	go t.updateBucket(time.Now())
}

//Used for internal testing. Controlling the ticker means being able to test per-tick rather than per-duration,
//which is more reliable if the app is under a lot of stress
func (t *TokenTicker) start(ticker *time.Ticker, startTime time.Time) {
	if t.ticker != nil {
		t.Stop()
	}

	t.ticker = ticker
	//Ticker goroutine: ticks every 500ms to check whether tokens can be added to the bucket or not
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
			break
		} else if atomic.CompareAndSwapInt64(&t.tokens, tokens, tokens-1) {
			return true
		}
	}

	return false
}
