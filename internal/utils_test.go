// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func BenchmarkIter(b *testing.B) {
	m := NewLockMap(nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Iter(func(key string, val string) {})
	}
}

func TestLockMapThrash(t *testing.T) {
	wg := sync.WaitGroup{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	t.Cleanup(cancel)
	lm := NewLockMap(map[string]string{})
	wg.Add(6)
	for i := 0; i < 3; i++ {
		// Readers
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					lm.Iter(func(key string, val string) {
						_ = key + val //fake work
					})
				}
			}
		}()
		// Writers
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					lm.Set(strings.Repeat("a", rand.Int()%10), "val")
					if rand.Int()%3 == 0 {
						lm.Clear()
					}
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, len(lm.m), int(lm.c))
}

func TestXSyncMapCounterMap(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)

		cm := NewXSyncMapCounterMap()

		assert.Equal(map[string]int64{}, cm.GetAndReset())

		cm.Inc("a")
		cm.Inc("b")
		cm.Inc("a")

		assert.Equal(map[string]int64{"a": 2, "b": 1}, cm.GetAndReset())

		cm.Inc("a")
		assert.Equal(map[string]int64{"a": 1}, cm.GetAndReset())
	})

	t.Run("concurrent", func(t *testing.T) {
		assert := assert.New(t)

		cm := NewXSyncMapCounterMap()

		wg := sync.WaitGroup{}
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cm.Inc("key")
			}()
		}
		wg.Wait()

		assert.Equal(map[string]int64{"key": 10}, cm.GetAndReset())
	})
}

func BenchmarkXSyncMapCounterMap(b *testing.B) {
	b.Run("base_case", func(b *testing.B) {
		b.ReportAllocs()
		n := 10
		keys := make([]string, n)
		for i := range keys {
			keys[i] = "key-" + strconv.Itoa(i)
		}

		b.ResetTimer()
		cm := NewXSyncMapCounterMap()
		for i := 0; i < b.N; i++ {
			// We increment the first key w 75% probability and the rest
			// increment the rest of the keys.
			// This is to benchmark the expected case of most spans starting
			// from the same one integration, with less starting from other sources.
			if i%4 == 0 {
				cm.Inc(keys[i/4%n])
			} else {
				cm.Inc(keys[0])
			}
		}

		// Ensure that the values in the map are as expected (monotically decreasing)
		counts := cm.GetAndReset()
		for i := 1; i < n; i++ {
			assert.LessOrEqual(b, counts[keys[i]], counts[keys[i-1]])
		}
	})

	b.Run("worst_case", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		cm := NewXSyncMapCounterMap()
		for i := 0; i < b.N; i++ {
			cm.Inc("key-" + strconv.Itoa(i))
		}

		// Ensure all counts are exactly 1
		counts := cm.GetAndReset()
		for _, v := range counts {
			assert.Equal(b, int64(1), v)
		}

	})

	b.Run("concurrent", func(b *testing.B) {
		cm := NewXSyncMapCounterMap()

		wg := sync.WaitGroup{}
		for range b.N {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cm.Inc("key")
			}()
		}
		wg.Wait()

		assert.Equal(b, map[string]int64{"key": int64(b.N)}, cm.GetAndReset())
	})
}
