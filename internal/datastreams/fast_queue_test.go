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
	q := newFastQueue(defaultQueueSize)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 2}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 3}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 4}}))
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Equal(t, uint64(4), q.pop().point.hash)
	for i := range defaultQueueSize {
		assert.False(t, q.push(&processorInput{point: statsPoint{hash: uint64(i)}}))
		assert.Equal(t, uint64(i), q.pop().point.hash)
	}
}

func TestFastQueueConfigurableSize(t *testing.T) {
	q := newFastQueue(3)
	assert.Equal(t, int64(3), q.size)
	assert.Len(t, q.elements, 3)
}

func TestFastQueueDefaultsWhenSizeNotPositive(t *testing.T) {
	for _, size := range []int{0, -1} {
		q := newFastQueue(size)
		assert.Equal(t, int64(defaultQueueSize), q.size)
	}
}

func TestFastQueueRejectsWhenFull(t *testing.T) {
	q := newFastQueue(2)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 2}}))
	// queue is now full; the unread items must not be overwritten.
	assert.True(t, q.push(&processorInput{point: statsPoint{hash: 3}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.Nil(t, q.pop())
	// a slot freed up by the pop is available again.
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 4}}))
	assert.Equal(t, uint64(4), q.pop().point.hash)
}
