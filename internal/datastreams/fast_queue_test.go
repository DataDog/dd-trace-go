// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFastQueue(t *testing.T) {
	q := newFastQueue()
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 2}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 3}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 4}}))
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Equal(t, uint64(4), q.pop().point.hash)
	for i := 0; i < queueSize; i++ {
		assert.False(t, q.push(&processorInput{point: statsPoint{hash: uint64(i)}}))
		assert.Equal(t, uint64(i), q.pop().point.hash)
	}
}

func TestNoDoubleReads(t *testing.T) {
	var wg sync.WaitGroup
	var q = newFastQueue()
	var countPerG = 1000
	for i := 0; i < countPerG*runtime.GOMAXPROCS(0); i++ {
		q.push(&processorInput{point: statsPoint{hash: uint64(i)}})
	}

	var seenPerG = make([]map[uint64]struct{}, runtime.GOMAXPROCS(0))
	for g := 0; g < runtime.GOMAXPROCS(0); g++ {
		wg.Add(1)
		g := g
		seenPerG[g] = make(map[uint64]struct{})
		go func() {
			defer wg.Done()
			for i := 0; i < countPerG; i++ {
				val := q.pop()
				if val == nil {
					continue
				}
				seenPerG[g][val.point.hash] = struct{}{}
			}
		}()
	}
	wg.Wait()

	var seen = make(map[uint64]struct{})
	for _, v := range seenPerG {
		for k := range v {
			if _, ok := seen[k]; ok {
				t.Fatalf("double read of %d", k)
			}
			seen[k] = struct{}{}
		}
	}
}
