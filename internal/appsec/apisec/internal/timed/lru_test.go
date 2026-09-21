// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package timed

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"testing/synctest"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec/internal/config"

	"github.com/stretchr/testify/require"
)

func TestLRU(t *testing.T) {
	t.Run("NewLRU", func(t *testing.T) {
		require.PanicsWithError(t, "NewLRU: interval must be <= 1193046h28m15s, but was 1193046h28m16s", func() {
			NewLRU(time.Second * (math.MaxUint32 + 1))
		})
	})

	t.Run("Hit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const sampleIntervalSeconds = 30
			subject := NewLRU(sampleIntervalSeconds * time.Second)

			require.True(t, subject.Hit(1337))
			for range sampleIntervalSeconds {
				require.False(t, subject.Hit(1337))
				time.Sleep(time.Second)
			}
			require.True(t, subject.Hit(1337))

			require.True(t, subject.Hit(0))

			// Keys are slotted via [% capacity], so if we don't properly encode
			// 0-values, the new slot will inherit the previously set sample time, and
			// the assertion will fail as a result.
			zeroSlot := uint64(capacity)
			if zeroSlot == subject.zeroKey {
				// There is a very small chance that the zero key has been set to
				// [capacity], in which case we'll just double it to escape the
				// collision and get a fresh new hit.
				zeroSlot *= 2
			}
			require.True(t, subject.Hit(zeroSlot))
		})
	})

	t.Run("rebuild", func(t *testing.T) {
		goCount := runtime.GOMAXPROCS(0) * 10

		synctest.Test(t, func(t *testing.T) {
			subject := NewLRU(30 * time.Second)

			var (
				startBarrier  sync.WaitGroup
				finishBarrier sync.WaitGroup
			)
			startBarrier.Add(goCount + 1)
			for range goCount {
				finishBarrier.Go(func() {
					startBarrier.Done()
					startBarrier.Wait()

					for key := range uint64(config.MaxItemCount * 4) {
						_ = subject.Hit(key)
						time.Sleep(time.Second)
					}
				})
			}

			startBarrier.Done()
			finishBarrier.Wait()

			// Wait for an in-progress rebuild to finish...
			for subject.rebuilding.Load() {
				runtime.Gosched()
			}

			// Check the final table has a reasonable content...
			table := subject.table.Load()
			count := 0
			for i := range table.entries {
				entry := &table.entries[i]
				if entry.Key.Load() == 0 {
					continue
				}
				// Since we ran through the keys sequentially, we should not have kept any
				// of the first [config.MaxItemCount] keys in any case.
				require.Less(t, uint64(config.MaxItemCount), entry.Key.Load())
				count++
			}
			// We should not have more than [maxItemCount] items left in the map...
			require.LessOrEqual(t, count, config.MaxItemCount)
		})
	})
}

// TestLRUConcurrentSameKeyCount is a regression test for the count leak that occurred when several
// goroutines raced to claim the same empty slot for the same key on first insert: only one wins the
// slot CAS, and each loser must release the speculative count increment it made. Without that
// release the table count is permanently inflated, triggering premature eviction/capacity rejection.
// A single key maps to a single slot, so exactly one slot must end up occupied and count must be 1.
func TestLRUConcurrentSameKeyCount(t *testing.T) {
	const key = uint64(0x9e3779b97f4a7c15) // non-zero so it is not remapped onto zeroKey
	const goroutines = 256

	subject := NewLRU(30 * time.Second)

	var start, done sync.WaitGroup
	start.Add(1)
	for range goroutines {
		done.Go(func() {
			start.Wait() // release all goroutines together to maximise the slot-claim race
			subject.Hit(key)
		})
	}
	start.Done()
	done.Wait()

	table := subject.table.Load()
	occupied := 0
	for i := range table.entries {
		if table.entries[i].Key.Load() != 0 {
			occupied++
		}
	}
	require.Equal(t, 1, occupied, "exactly one slot must be occupied for a single key")
	require.Equal(t, int32(1), table.count.Load(),
		"table.count must equal the number of occupied slots; a lost same-key slot race must not inflate it")
}
