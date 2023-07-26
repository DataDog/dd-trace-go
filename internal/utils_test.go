// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"context"
	"math/rand"
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
	ctx, _ := context.WithTimeout(context.Background(), 20*time.Millisecond)
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
