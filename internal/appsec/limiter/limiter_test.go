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
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestLimiterUnit(t *testing.T) {
	t.Run("no-ticks-1", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(1, 100)
			require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
			require.False(t, l.Allow(), "Second call to limiter.Allow() should return False")
		})
	})

	t.Run("no-ticks-2", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(100, 100)
			for range 100 {
				require.True(t, l.Allow())
			}
			require.False(t, l.Allow())
		})
	})

	t.Run("10ms-ticks", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(1, 100)
			require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
			require.False(t, l.Allow(), "Second call to limiter.Allow() should return false")
			time.Sleep(10 * time.Millisecond)
			require.True(t, l.Allow(), "Third call to limiter.Allow() after 10ms should return True")
		})
	})

	t.Run("9ms-ticks", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(1, 100)
			require.True(t, l.Allow(), "First call to limiter.Allow() should return True")
			time.Sleep(9 * time.Millisecond)
			require.False(t, l.Allow(), "Second call to limiter.Allow() after 9ms should return False")
			time.Sleep(10 * time.Millisecond)
			require.True(t, l.Allow(), "Third call to limiter.Allow() after 10ms should return True")
		})
	})

	t.Run("1s-rate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(1, 1)
			require.True(t, l.Allow(), "First call to limiter.Allow() should return True with 1s per token")
			time.Sleep(500 * time.Millisecond)
			require.False(t, l.Allow(), "Second call to limiter.Allow() should return False with 1s per Token")
			time.Sleep(1000 * time.Millisecond)
			require.True(t, l.Allow(), "Third call to limiter.Allow() should return True with 1s per Token")
		})
	})

	t.Run("100-requests-burst", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(100, 100)
			for i := range 100 {
				require.Truef(t, l.Allow(),
					"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
				time.Sleep(50 * time.Millisecond)
			}
		})
	})

	t.Run("101-requests-burst", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(100, 100)
			for i := range 100 {
				require.Truef(t, l.Allow(),
					"Burst call %d to limiter.Allow() should return True with 100 initial tokens", i)
				time.Sleep(50 * time.Microsecond)
			}
			require.False(t, l.Allow(),
				"Burst call 101 to limiter.Allow() should return False with 100 initial tokens")
		})
	})

	t.Run("bucket-refill-short", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(100, 100)
			time.Sleep(time.Millisecond)
			require.Equal(t, 100, drain(l))
		})
	})

	t.Run("bucket-refill-long", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(100, 100)
			time.Sleep(3 * time.Second)
			require.Equal(t, 100, drain(l))
		})
	})

	t.Run("exhaust-burst-no-refill", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(3, 3)
			require.True(t, l.Allow())
			require.True(t, l.Allow())
			require.True(t, l.Allow())
			require.False(t, l.Allow())
		})
	})

	t.Run("refill-after-empty", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTicker(2, 100)
			require.True(t, l.Allow())
			require.True(t, l.Allow())
			require.False(t, l.Allow())
			require.False(t, l.Allow())
			time.Sleep(10 * time.Millisecond)
			require.True(t, l.Allow())
		})
	})
}

func drain(l Limiter) int {
	n := 0
	for l.Allow() {
		n++
	}
	return n
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
				var skipped, kept atomic.Uint64
				l := NewTokenTicker(0, 100)

				for range nbUsers {
					stopBarrier.Go(func() {
						startBarrier.Wait() // Sync the starts of the goroutines

						for tStart := time.Now(); time.Since(tStart) < 1*time.Second; {
							if !l.Allow() {
								skipped.Add(1)
							} else {
								kept.Add(1)
							}
						}
					})
				}

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
		for burstAmount := 1; burstAmount <= 10; burstAmount++ {
			t.Run(fmt.Sprintf("requests-bursts-%d-iterations", burstAmount), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					skipped := 0
					kept := 0
					l := NewTokenTicker(100, 100)

					for c := 0; c < burstAmount; c++ {
						for range burstSize {
							if !l.Allow() {
								skipped++
							} else {
								kept++
							}
						}
						time.Sleep(burstFreq)
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

			for b.Loop() {
				var startBarrier, stopBarrier sync.WaitGroup
				// Create a start barrier to synchronize every goroutine's launch and
				// increase the chances of parallel accesses
				startBarrier.Add(1)
				// Create a stopBarrier to signal when all user goroutines are done.

				for range nbUsers {
					stopBarrier.Go(func() {
						startBarrier.Wait() // Sync the starts of the goroutines

						for range 100 {
							if !limiter.Allow() {
								skipped.Add(1)
							} else {
								kept.Add(1)
							}
						}
					})
				}

				startBarrier.Done() // Unblock the user goroutines
				stopBarrier.Wait()  // Wait for the user goroutines to be done
			}

			assert.NotEqual(b, 0, kept.Load(), "expected to have accepted at least 1")
			assert.NotEqual(b, 0, skipped.Load(), "expected to have skipped at least 1")
		})
	}
}

func TestLimiterWithInterval(t *testing.T) {
	t.Run("60-per-minute-rate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTickerWithInterval(1, 60, time.Minute)
			require.True(t, l.Allow(), "First call should be allowed")
			require.False(t, l.Allow(), "Second call should be disallowed")

			time.Sleep(500 * time.Millisecond)
			require.False(t, l.Allow(), "A call after 0.5s should be disallowed")

			time.Sleep(500 * time.Millisecond)
			require.True(t, l.Allow(), "A call after 1s should be allowed")
			require.False(t, l.Allow(), "Another call should be disallowed")
		})
	})

	t.Run("1-per-100ms-rate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := NewTokenTickerWithInterval(1, 1, 100*time.Millisecond)
			require.True(t, l.Allow(), "First call should be allowed")
			require.False(t, l.Allow(), "Second call should be disallowed")

			time.Sleep(50 * time.Millisecond)
			require.False(t, l.Allow(), "A call after 50ms should be disallowed")

			time.Sleep(50 * time.Millisecond)
			require.True(t, l.Allow(), "A call after 100ms should be allowed")
			require.False(t, l.Allow(), "Another call should be disallowed")
		})
	})
}
