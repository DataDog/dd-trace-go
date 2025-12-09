// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022-present Datadog, Inc.

package limiter

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestLimiterUnit(t *testing.T) {
	startTime := time.Now()

	t.Run("no-ticks-1", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(1, 100)
		l.start(startTime)
		defer l.stop()
		// No ticks between the requests
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False")
	})

	t.Run("no-ticks-2", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(100, 100)
		l.start(startTime)
		defer l.stop()
		// No ticks between the requests
		for i := 0; i < 100; i++ {
			require.True(t, l.Allow())
		}
		require.False(t, l.Allow())
	})

	t.Run("10ms-ticks", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(1, 100)
		l.start(startTime)
		defer l.stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return false")
		l.tick(10 * time.Millisecond)
		require.True(t, l.Allow(), "Third call to limiter.Allow() after 10ms should return True")
	})

	t.Run("9ms-ticks", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(1, 100)
		l.start(startTime)
		defer l.stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
		l.tick(9 * time.Millisecond)
		require.False(t, l.Allow(), "Second call to limiter.Allow() after 9ms should return False")
		l.tick(10 * time.Millisecond)
		require.True(t, l.Allow(), "Third call to limiter.Allow() after 10ms should return True")
	})

	t.Run("1s-rate", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(1, 1)
		l.start(startTime)
		defer l.stop()
		require.True(t, l.Allow(), "First call to limiter.Allow() should return True with 1s per token")
		l.tick(500 * time.Millisecond)
		require.False(t, l.Allow(), "Second call to limiter.Allow() should return False with 1s per Token")
		l.tick(1000 * time.Millisecond)
		require.True(t, l.Allow(), "Third call to limiter.Allow() should return True with 1s per Token")
	})

	t.Run("100-requests-burst", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(100, 100)
		l.start(startTime)
		defer l.stop()
		for i := 0; i < 100; i++ {
			require.Truef(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			l.tick(50 * time.Millisecond)
		}
	})

	t.Run("101-requests-burst", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(100, 100)
		l.start(startTime)
		defer l.stop()
		for i := 0; i < 100; i++ {
			require.Truef(t, l.Allow(),
				"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
			startTime = startTime.Add(50 * time.Microsecond)
			l.tick(0)
		}
		require.False(t, l.Allow(),
			"Burst call 101 to limiter.Allow() should return False with 100 initial tokens")
	})

	t.Run("bucket-refill-short", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(100, 100)
		l.start(startTime)
		defer l.stop()

		for i := 0; i < 1000; i++ {
			l.tick(time.Millisecond)
			require.Equalf(t, int64(100), l.t.tokens.Load(), "Bucket should have exactly 100 tokens")
		}
	})

	t.Run("bucket-refill-long", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(100, 100)
		l.start(startTime)
		defer l.stop()

		for i := 0; i < 1000; i++ {
			l.tick(3 * time.Second)
		}
		require.Equalf(t, int64(100), l.t.tokens.Load(), "Bucket should have exactly 100 tokens")
	})

	t.Run("allow-after-stop", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(3, 3)
		l.start(startTime)
		require.True(t, l.Allow())
		l.stop()
		// The limiter keeps allowing until there's no more tokens
		require.True(t, l.Allow())
		require.True(t, l.Allow())
		require.False(t, l.Allow())
	})

	t.Run("allow-before-start", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTicker(2, 100)
		// The limiter keeps allowing until there's no more tokens
		require.True(t, l.Allow())
		require.True(t, l.Allow())
		require.False(t, l.Allow())
		l.start(startTime)
		// The limiter has used all its tokens and the bucket is not getting refilled yet
		require.False(t, l.Allow())
		l.tick(10 * time.Millisecond)
		// The limiter has started refilling its tokens
		require.True(t, l.Allow())
		l.stop()
	})
}

func TestLimiter(t *testing.T) {
	t.Run("concurrency", func(t *testing.T) {
		// Tests the limiter's ability to sample the traces when subjected to a continuous flow of requests
		// Each goroutine will continuously call the rate limiter for 1 second
		for nbUsers := 1; nbUsers <= 10; nbUsers *= 10 {
			t.Run(fmt.Sprintf("continuous-requests-%d-users", nbUsers), func(t *testing.T) {
				defer goleak.VerifyNone(t)

				var startBarrier, stopBarrier sync.WaitGroup
				// Create a start barrier to synchronize every goroutine's launch and
				// increase the chances of parallel accesses
				startBarrier.Add(1)
				// Create a stopBarrier to signal when all user goroutines are done.
				stopBarrier.Add(nbUsers)
				var skipped, kept atomic.Uint64
				l := NewTokenTicker(0, 100)

				for n := 0; n < nbUsers; n++ {
					go func(l Limiter, kept, skipped *atomic.Uint64) {
						startBarrier.Wait()      // Sync the starts of the goroutines
						defer stopBarrier.Done() // Signal we are done when returning

						for tStart := time.Now(); time.Since(tStart) < 1*time.Second; {
							if !l.Allow() {
								skipped.Add(1)
							} else {
								kept.Add(1)
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
				maxExpectedKept := uint64(math.Ceil(duration) * 100)

				require.LessOrEqualf(t, kept.Load(), maxExpectedKept,
					"Expected at most %d kept tokens for a %fs duration", maxExpectedKept, duration)
			})
		}

		burstFreq := 1000 * time.Millisecond
		burstSize := 101
		startTime := time.Now()
		// Simulate sporadic bursts during up to 1 minute
		for burstAmount := 1; burstAmount <= 10; burstAmount++ {
			t.Run(fmt.Sprintf("requests-bursts-%d-iterations", burstAmount), func(t *testing.T) {
				defer goleak.VerifyNone(t)

				skipped := 0
				kept := 0
				l := newTestTicker(100, 100)
				l.start(startTime)
				defer l.stop()

				for c := 0; c < burstAmount; c++ {
					for i := 0; i < burstSize; i++ {
						if !l.Allow() {
							skipped++
						} else {
							kept++
						}
					}
					// Schedule next burst 1sec later
					l.tick(burstFreq)
				}

				expectedSkipped := (burstSize - 100) * burstAmount
				expectedKept := 100 * burstAmount
				if burstSize < 100 {
					expectedSkipped = 0
					expectedKept = burstSize * burstAmount
				}
				require.Equalf(t, kept, expectedKept, "Expected %d burst requests to be kept", expectedKept)
				require.Equalf(t, expectedSkipped, skipped, "Expected %d burst requests to be skipped", expectedSkipped)
			})
		}
	})
}

func BenchmarkLimiter(b *testing.B) {
	defer goleak.VerifyNone(b, goleak.IgnoreCurrent())

	for nbUsers := 1; nbUsers <= 1000; nbUsers *= 10 {
		b.Run(fmt.Sprintf("%d-users", nbUsers), func(b *testing.B) {
			var skipped, kept atomic.Uint64
			limiter := NewTokenTicker(0, 100)
			limiter.Start()
			defer limiter.Stop()

			b.StopTimer()
			b.ResetTimer()

			for n := 0; n < b.N; n++ {
				var startBarrier, stopBarrier sync.WaitGroup
				// Create a start barrier to synchronize every goroutine's launch and
				// increase the chances of parallel accesses
				startBarrier.Add(1)
				// Create a stopBarrier to signal when all user goroutines are done.
				stopBarrier.Add(nbUsers)

				for n := 0; n < nbUsers; n++ {
					go func(l Limiter, kept, skipped *atomic.Uint64) {
						startBarrier.Wait()      // Sync the starts of the goroutines
						defer stopBarrier.Done() // Signal we are done when returning

						b.StartTimer() // Ensure the timer is started now...

						for i := 0; i < 100; i++ {
							if !l.Allow() {
								skipped.Add(1)
							} else {
								kept.Add(1)
							}
						}
					}(limiter, &kept, &skipped)
				}

				startBarrier.Done() // Unblock the user goroutines
				stopBarrier.Wait()  // Wait for the user goroutines to be done
				b.StopTimer()
			}

			assert.NotEqual(b, 0, kept.Load(), "expected to have accepted at least 1")
			assert.NotEqual(b, 0, skipped.Load(), "expected to have skipped at least 1")
		})
	}
}

// TestTicker is a utility struct used to send hand-crafted ticks to the rate limiter for controlled testing
// It also makes sure to give time to the bucket update goroutine by using the optional sync channel
type TestTicker struct {
	C         chan time.Time
	syncChan  <-chan struct{}
	t         *TokenTicker
	timestamp time.Time
}

func newTestTicker(tokens, maxTokens int64) *TestTicker {
	return &TestTicker{
		C: make(chan time.Time),
		t: NewTokenTicker(tokens, maxTokens),
	}
}

func (t *TestTicker) start(timestamp time.Time) {
	syncChan := make(chan struct{}, 1)
	t.syncChan = syncChan
	t.timestamp = timestamp
	t.t.start(timestamp, t.C, syncChan)
}

func (t *TestTicker) stop() {
	t.t.Stop()
	close(t.C)
	// syncChan is closed by the token ticker when sure that nothing else will be sent on it.
	for _, ok := <-t.syncChan; ok; _, ok = <-t.syncChan {
		// Drain the channel.
	}
	t.syncChan = nil
	t.timestamp = time.Time{}
}

// tick advances the `TestTicker`'s internal clock by the provided duration, and sends a tick to the
// underlying `TokenTicker`. It then waits for the `TokenTicker` to be done processing that tick, so
// the caller can assume the tocken bucket has been appropriately updated.
func (t *TestTicker) tick(delta time.Duration) {
	t.timestamp = t.timestamp.Add(delta)

	t.C <- t.timestamp
	<-t.syncChan
}

func (t *TestTicker) Allow() bool {
	return t.t.Allow()
}

func newTestTickerWithInterval(tokens, maxTokens int64, interval time.Duration) *TestTicker {
	return &TestTicker{
		C: make(chan time.Time),
		t: NewTokenTickerWithInterval(tokens, maxTokens, interval),
	}
}

func TestLimiterWithInterval(t *testing.T) {
	startTime := time.Now()
	t.Run("60-per-minute-rate", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTickerWithInterval(1, 60, time.Minute) // 60 tokens per minute, so 1 per second
		l.start(startTime)
		defer l.stop()
		require.True(t, l.Allow(), "First call should be allowed")
		require.False(t, l.Allow(), "Second call should be disallowed")

		l.tick(500 * time.Millisecond)
		require.False(t, l.Allow(), "A call after 0.5s should be disallowed")

		l.tick(500 * time.Millisecond) // Total 1 second passed
		require.True(t, l.Allow(), "A call after 1s should be allowed")
		require.False(t, l.Allow(), "Another call should be disallowed")
	})

	t.Run("1-per-100ms-rate", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		l := newTestTickerWithInterval(1, 1, 100*time.Millisecond) // 1 token per 100ms
		l.start(startTime)
		defer l.stop()
		require.True(t, l.Allow(), "First call should be allowed")
		require.False(t, l.Allow(), "Second call should be disallowed")

		l.tick(50 * time.Millisecond)
		require.False(t, l.Allow(), "A call after 50ms should be disallowed")

		l.tick(50 * time.Millisecond) // Total 100ms passed
		require.True(t, l.Allow(), "A call after 100ms should be allowed")
		require.False(t, l.Allow(), "Another call should be disallowed")
	})
}
