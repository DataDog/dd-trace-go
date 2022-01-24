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
	ticker := TestTicker{C: make(chan time.Time)}
	defer close(ticker.C)
	startTime := time.Now()

	t.Run("no-ticks-1", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		// No ticks between the requests
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False")
	})

	t.Run("no-ticks-2", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		// No ticks between the requests
		for i := 0; i < 10; i++ {
			require.True(t, l.Allow(), "Call to limiter.Allow() should return True")
		}
	})

	t.Run("10ms-ticks", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		ticker.tick(startTime.Add(10 * time.Millisecond))
		require.True(t, l.Allow(), "Second call to limiter.Allow() after 10ms should return True")
	})

	t.Run("9ms-ticks", func(t *testing.T) {
		l := NewTokenTicker(1, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		ticker.tick(startTime.Add(9 * time.Millisecond))
		require.False(t, l.Allow(), "Second call to limiter.Allow() after 9ms should return False")
		ticker.tick(startTime.Add(18 * time.Millisecond))
		require.True(t, l.Allow(), "Third call to limiter.Allow() after 18ms should return True")
	})

	t.Run("1s-rate", func(t *testing.T) {
		l := NewTokenTicker(1, 1)
		l.start(ticker.C, startTime)
		defer l.Stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True with 1s per token")
		ticker.tick(startTime.Add(500 * time.Millisecond))
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False with 1s per Token")
		ticker.tick(startTime.Add(1000 * time.Millisecond))
		require.True(t, l.Allow(), "Third call to limiter.Allow() should return True with 1s per Token")
	})

	t.Run("100-requests-burst", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		for i := 0; i < 100; i++ {
			require.Truef(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			startTime = startTime.Add(50 * time.Microsecond)
			ticker.tick(startTime)
		}
	})

	t.Run("101-requests-burst", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()
		for i := 0; i < 100; i++ {
			require.Truef(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			startTime = startTime.Add(50 * time.Microsecond)
			ticker.tick(startTime)
		}
		require.False(t, l.Allow(),
			"Burst call 101 to limiter.Allow() should return False with 100 initial tokens")
	})

	t.Run("bucket-refill-short", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()

		for i := 0; i < 1000; i++ {
			startTime = startTime.Add(time.Millisecond)
			ticker.tick(startTime)
			require.Equalf(t, int64(100), atomic.LoadInt64(&l.tokens), "Bucket should have exactly 100 tokens")
		}
	})

	t.Run("bucket-refill-long", func(t *testing.T) {
		l := NewTokenTicker(100, 100)
		l.start(ticker.C, startTime)
		defer l.Stop()

		for i := 0; i < 1000; i++ {
			startTime = startTime.Add(3 * time.Second)
			ticker.tick(startTime)
		}
		require.Equalf(t, int64(100), atomic.LoadInt64(&l.tokens), "Bucket should have exactly 100 tokens")
	})

	t.Run("allow-after-stop", func(t *testing.T) {
		l := NewTokenTicker(3, 3)
		l.start(ticker.C, startTime)
		require.True(t, l.Allow())
		l.Stop()
		// The limiter keeps allowing until there's no more tokens
		require.True(t, l.Allow())
		require.True(t, l.Allow())
		require.False(t, l.Allow())
	})

	t.Run("allow-before-start", func(t *testing.T) {
		l := NewTokenTicker(2, 100)
		// The limiter keeps allowing until there's no more tokens
		require.True(t, l.Allow())
		require.True(t, l.Allow())
		require.False(t, l.Allow())
		l.start(ticker.C, startTime)
		// The limiter has used all its tokens and the bucket is not getting refilled yet
		require.False(t, l.Allow())
		ticker.tick(startTime.Add(10 * time.Millisecond))
		// The limiter has started refilling its tokens
		require.True(t, l.Allow())
		l.Stop()
	})
}

func TestLimiter(t *testing.T) {
	t.Run("concurrency", func(t *testing.T) {
		//Tests the limiter's ability to sample the traces when subjected to a continuous flow of requests
		//Each goroutine will continuously call the rate limiter for 1 second
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
				l := NewTokenTicker(0, 100)

				for n := 0; n < nbUsers; n++ {
					go func(l Limiter, kept *int64, skipped *int64) {
						startBarrier.Wait()      // Sync the starts of the goroutines
						defer stopBarrier.Done() // Signal we are done when returning

						for tStart := time.Now(); time.Since(tStart) < 1*time.Second; {
							if !l.Allow() {
								atomic.AddInt64(skipped, 1)
							} else {
								atomic.AddInt64(kept, 1)
							}
						}
					}(l, &kept, &skipped)
				}

				l.Start()
				defer l.Stop()
				start := time.Now()
				startBarrier.Done() // Unblock the user goroutines
				stopBarrier.Wait()  // Wait for the user goroutines to be done
				duration := time.Since(start).Seconds()
				maxExpectedKept := int64(math.Ceil(duration) * 100)

				require.LessOrEqualf(t, kept, maxExpectedKept,
					"Expected at most %d kept tokens for a %fs duration", maxExpectedKept, duration)
			})
		}

		// Tests the limiter's ability to sample the traces when subjected to sporadic bursts of requests.
		// The limiter starts with a bucket filled with 100 tokens to handle a potential immediate first burst
		ticker := TestTicker{C: make(chan time.Time)}
		defer close(ticker.C)
		burstFreq := 1000 * time.Millisecond
		burstSize := 101
		startTime := time.Now()
		// Simulate sporadic bursts during up to 1 minute
		for burstAmount := 1; burstAmount <= 10; burstAmount++ {
			t.Run(fmt.Sprintf("requests-bursts-%d-iterations", burstAmount), func(t *testing.T) {
				skipped := 0
				kept := 0
				l := NewTokenTicker(100, 100)
				l.start(ticker.C, startTime)
				defer l.Stop()

				for c := 0; c < burstAmount; c++ {
					for i := 0; i < burstSize; i++ {
						if !l.Allow() {
							skipped++
						} else {
							kept++
						}
					}
					// Schedule next burst 1sec later
					startTime = startTime.Add(burstFreq)
					ticker.tick(startTime)
				}

				expectedSkipped := (burstSize - 100) * burstAmount
				expectedKept := 100 * burstAmount
				if burstSize < 100 {
					expectedSkipped = 0
					expectedKept = burstSize * burstAmount
				}
				require.Equalf(t, kept, expectedKept, "Expected at most %d burst requests to be kept", expectedKept)
				require.Equalf(t, expectedSkipped, skipped, "Expected at least %d burst requests to be skipped", expectedSkipped)
			})
		}
	})
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

// TestTicker is a utility struct used to send hand-crafted ticks to the rate limiter for controlled testing
// It also makes sure to give time to the bucket update goroutine to be scheduled by sleeping for a while after sending
// a tick
type TestTicker struct {
	C chan time.Time
}

func (t *TestTicker) tick(timeStamp time.Time) {
	t.C <- timeStamp
	time.Sleep(1 * time.Millisecond)
}
