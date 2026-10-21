// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"testing"
	"time"

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

func TestFastQueueDrainWaitsForStalledWriter(t *testing.T) {
	q := newFastQueue(4)
	reserved := q.writePos.Add(1)
	q.push(&processorInput{point: statsPoint{hash: 2}})

	var got []uint64
	collect := func(in *processorInput) { got = append(got, in.point.hash) }

	q.drain(time.Now().Add(-time.Second), collect)
	assert.Empty(t, got)
	assert.Equal(t, int64(0), q.readPos.Load())

	stalled := &processorInput{point: statsPoint{hash: 1}, queuePos: reserved - 1}
	q.elements[(reserved-1)%q.size].Store(stalled)
	q.drain(time.Now().Add(time.Second), collect)
	assert.Equal(t, []uint64{1, 2}, got)
}

func TestFastQueueDrainEmpty(t *testing.T) {
	q := newFastQueue(4)
	called := false
	q.drain(time.Now().Add(time.Hour), func(*processorInput) { called = true })
	assert.False(t, called)
}

func TestFastQueueDrainSkipsLaterPushes(t *testing.T) {
	q := newFastQueue(4)
	q.push(&processorInput{point: statsPoint{hash: 1}})

	var got []uint64
	q.drain(time.Now().Add(time.Second), func(in *processorInput) {
		got = append(got, in.point.hash)
		q.push(&processorInput{point: statsPoint{hash: in.point.hash + 1}})
	})
	assert.Equal(t, []uint64{1}, got)
	assert.Equal(t, uint64(2), q.pop().point.hash)
}
