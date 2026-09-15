// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFastQueue(t *testing.T) {
	const size = 100

	q := newFastQueueSize(size)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 2}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 3}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 4}}))
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Equal(t, uint64(4), q.pop().point.hash)
	// A full lap, so every slot is written and read at a wrapped index.
	for i := range size {
		assert.False(t, q.push(&processorInput{point: statsPoint{hash: uint64(i)}}))
		assert.Equal(t, uint64(i), q.pop().point.hash)
	}
}

// Dropping is what the size ultimately buys, and the historical 10,000-slot default made
// it impractical to exercise directly.
func TestFastQueueDropsOnceFull(t *testing.T) {
	const size = 4

	q := newFastQueueSize(size)
	for i := range size {
		assert.False(t, q.push(&processorInput{point: statsPoint{hash: uint64(i)}}))
	}

	// dropped reports that more values are outstanding than the ring can hold, so the
	// oldest has been overwritten before the reader got to it.
	assert.True(t, q.push(&processorInput{point: statsPoint{hash: 99}}))

	// Draining brings the outstanding count back under capacity. Two reads rather than
	// one: the push above already overwrote the oldest slot, so six values have been
	// written and only four of them can be outstanding at once.
	assert.NotNil(t, q.pop())
	assert.NotNil(t, q.pop())
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 100}}))
}

// The default path: whatever the host reports, the ring stays within the bounds the
// heuristic promises, and the slice matches the size the indexing arithmetic uses.
func TestNewFastQueueStaysWithinItsBounds(t *testing.T) {
	q := newFastQueue()

	// The resolved size depends on the host, so report it rather than assert it: this is
	// how `go test -v`, and a run under an explicit GOMEMLIMIT, show what the heuristic
	// actually decided.
	budget, found := memoryBudget("/")
	t.Logf("ring: %d slots (~%d bytes at capacity); budget %d bytes, found=%t", q.size, q.size*ringBytesPerSlot, budget, found)

	assert.GreaterOrEqual(t, q.size, int64(minRingSlots))
	assert.LessOrEqual(t, q.size, int64(maxRingSlots))
	assert.Len(t, q.elements, int(q.size))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
}

// The index moved from a compile-time constant modulo, which the compiler strength-reduces,
// to a division by a struct field, which it cannot. This is the measurement of that.
func BenchmarkFastQueuePush(b *testing.B) {
	q := newFastQueue()
	in := &processorInput{point: statsPoint{hash: 1}}

	for b.Loop() {
		q.push(in)
	}
}

// The real shape of the path: many writers, one reader.
func BenchmarkFastQueuePushParallel(b *testing.B) {
	q := newFastQueue()

	b.RunParallel(func(pb *testing.PB) {
		in := &processorInput{point: statsPoint{hash: 1}}
		for pb.Next() {
			q.push(in)
		}
	})
}
