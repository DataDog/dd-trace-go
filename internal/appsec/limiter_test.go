// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterUnit(t *testing.T) {
	ticksChan := make(chan time.Time)
	ticker := time.Ticker{C: ticksChan}
	startTime := time.Now()

	t.Run("no-ticks-1", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		//No ticks between the requests
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False")
	})

	t.Run("no-ticks-2", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		//No ticks between the requests
		for i := 0; i < 10; i++ {
			require.True(t, l.Allow(), "Call to limiter.Allow() should return True")
		}
	})

	t.Run("10ms-ticks", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		ticksChan <- startTime.Add(10 * time.Millisecond)
		require.True(t, l.Allow(), "Second call to limiter.Allow() after 11ms should return True")
	})

	t.Run("9ms-ticks", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		ticksChan <- startTime.Add(9 * time.Millisecond)
		require.False(t, l.Allow(), "Second call to limiter.Allow() after 9ms should return False")
	})

	t.Run("1s-rate", func(t *testing.T) {
		l := NewTokenTicker(1, 1)
		l.start(&ticker, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True with 1s per token")
		ticksChan <- startTime.Add(500 * time.Millisecond)
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False with 1s per Token")
	})

	t.Run("100-requests-burst", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		for i := 0; i < 100; i++ {
			require.True(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			startTime = startTime.Add(50 * time.Microsecond)
			ticksChan <- startTime
		}
	})

	t.Run("101-requests-burst", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(&ticker, startTime)
		defer l.Stop()
		for i := 0; i < 100; i++ {
			require.True(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			startTime = startTime.Add(50 * time.Microsecond)
			ticksChan <- startTime
		}
		require.False(t, l.Allow(),
			"Burst call 101 to limiter.Allow() should return False with 100 initial tokens")
	})

	ticker.Stop()
	close(ticksChan)
}

func TestLimiter(t *testing.T) {
	//Tests the limiter's ability to sample the traces when subjected to a continuous flow of requests
	//Each goroutine will continuously call the WAF and the rate limiter for 1 second
	for nbUsers := 1; nbUsers <= 1000; nbUsers *= 10 {
		t.Run(fmt.Sprintf("continuous-requests-%d-users", nbUsers), func(t *testing.T) {
			var startBarrier, stopBarrier sync.WaitGroup
			// Create a start barrier to synchronize every goroutine's launch and
			// increase the chances of parallel accesses
			startBarrier.Add(1)
			// Create a stopBarrier to signal when all user goroutines are done.
			stopBarrier.Add(nbUsers)
			skipped := int64(0)
			kept := int64(0)
			l := NewTokenTicker(1, 100)
			l.Start()
			defer l.Stop()

			for n := 0; n < nbUsers; n++ {
				go func(l Limiter, kept *int64, skipped *int64) {
					startBarrier.Wait()      // Sync the starts of the goroutines
					defer stopBarrier.Done() // Signal we are done when returning

					for tStart := time.Now(); time.Since(tStart) <= 1*time.Second; {
						if !l.Allow() {
							atomic.AddInt64(skipped, 1)
						} else {
							atomic.AddInt64(kept, 1)
						}
					}
				}(l, &kept, &skipped)
			}

			start := time.Now()
			startBarrier.Done() // Unblock the user goroutines
			stopBarrier.Wait()  // Wait for the user goroutines to be done
			duration := time.Since(start).Seconds()
			//Limiter started with 1 token, expecting a margin of error of 1
			maxExpectedKept := 1 + int64(math.Ceil(duration)*100)

			require.LessOrEqualf(t, kept, maxExpectedKept,
				"Expected at most %d kept tokens for a %fs duration", maxExpectedKept, duration)
		})
	}

	//Tests the limiter's ability to sample the traces when subjected sporadic bursts of requests.
	//The limiter starts with a bucket filled with 100 tokens to handle a potential immediate first burst
	for burstAmount := 10; burstAmount < 100; burstAmount += 10 {
		t.Run(fmt.Sprintf("requests-bursts-%d-iterations", burstAmount), func(t *testing.T) {
			burstFreq := 100 * time.Millisecond
			burstSize := 10
			skipped := 0
			kept := 0
			reqCount := 0
			l := NewTokenTicker(100, 100)
			l.Start()
			defer l.Stop()

			for c := 0; c < burstAmount; c++ {
				for i := 0; i < burstSize; i++ {
					reqCount++
					//Let's not run the WAF if we already know the limiter will ask to discard the trace
					if !l.Allow() {
						skipped++
					} else {
						kept++
					}
				}
				//Sleep until next burst
				time.Sleep(burstFreq)
			}
			require.Equalf(t, kept, burstAmount*burstSize, "Expected all %d burst requests to be kept", burstAmount*burstSize)
		})
	}
}

func BenchmarkLimiter(b *testing.B) {
	for nbUsers := 1; nbUsers <= 1000; nbUsers *= 10 {
		b.Run(fmt.Sprintf("%d-users", nbUsers), func(b *testing.B) {
			var skipped int64
			var kept int64
			limiter := NewTokenTicker(0, 100)
			limiter.Start()
			defer limiter.Stop()
			b.ResetTimer()

			for n := 0; n < b.N; n++ {
				var startBarrier, stopBarrier sync.WaitGroup
				// Create a start barrier to synchronize every goroutine's launch and
				// increase the chances of parallel accesses
				startBarrier.Add(1)
				// Create a stopBarrier to signal when all user goroutines are done.
				stopBarrier.Add(nbUsers)

				for n := 0; n < nbUsers; n++ {
					go func(l Limiter, kept *int64, skipped *int64) {
						startBarrier.Wait()      // Sync the starts of the goroutines
						defer stopBarrier.Done() // Signal we are done when returning

						for i := 0; i < 100; i++ {
							if !l.Allow() {
								atomic.AddInt64(skipped, 1)
							} else {
								atomic.AddInt64(kept, 1)
							}
						}
					}(limiter, &kept, &skipped)
				}
				startBarrier.Done() // Unblock the user goroutines
				stopBarrier.Wait()  // Wait for the user goroutines to be done
			}
		})
	}
}
