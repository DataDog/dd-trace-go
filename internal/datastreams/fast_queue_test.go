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
	q := newFastQueue(defaultIntakeBufferKB)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 1}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 2}}))
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 3}}))
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.False(t, q.push(&processorInput{point: statsPoint{hash: 4}}))
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Equal(t, uint64(4), q.pop().point.hash)
	for i := range 10000 {
		assert.False(t, q.push(&processorInput{point: statsPoint{hash: uint64(i)}}))
		assert.Equal(t, uint64(i), q.pop().point.hash)
	}
}

func TestFastQueueTracksBytes(t *testing.T) {
	q := newFastQueue(defaultIntakeBufferKB)
	assert.Zero(t, q.bytes())

	in := &processorInput{point: statsPoint{
		edgeTags:    []string{"direction:in", "type:kafka"},
		serviceName: "service",
	}}
	want := in.retainedBytes()
	assert.False(t, q.push(in))
	assert.Equal(t, want, q.bytes())

	assert.Equal(t, in, q.pop())
	assert.Zero(t, q.bytes())
}

func TestFastQueueRetainedBytesCountsIndirectMemory(t *testing.T) {
	bare := (&processorInput{}).retainedBytes()
	withTags := (&processorInput{point: statsPoint{edgeTags: []string{"topic:orders"}}}).retainedBytes()
	assert.Equal(t, bare+stringHeaderBytes+int64(len("topic:orders")), withTags)

	withTopic := (&processorInput{kafkaOffset: kafkaOffset{topic: "orders"}}).retainedBytes()
	assert.Equal(t, bare+int64(len("orders")), withTopic)
}

func TestFastQueueOverwritesOldestOnceBudgetReached(t *testing.T) {
	entry := func(hash uint64) *processorInput {
		return &processorInput{point: statsPoint{hash: hash, edgeTags: []string{"direction:in"}}}
	}
	perEntry := entry(0).retainedBytes()

	// A budget that holds three entries, expressed in KB as the config is.
	q := newFastQueue(defaultIntakeBufferKB)
	q.maxBytes = 3 * perEntry

	for i := range 3 {
		assert.False(t, q.push(entry(uint64(i))), "entry %d should fit", i)
	}
	assert.Equal(t, 3*perEntry, q.bytes())

	// The fourth entry is over budget, so the oldest is evicted to make room
	// and the queue stays at its budget.
	assert.True(t, q.push(entry(3)))
	assert.Equal(t, 3*perEntry, q.bytes())

	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Nil(t, q.pop())
	assert.Zero(t, q.bytes())
}

func TestFastQueueBudgetFromKB(t *testing.T) {
	kb := 4 * minIntakeBufferKB // comfortably above the floor
	q := newFastQueue(kb)
	assert.Equal(t, int64(kb*1024), q.maxBytes)
	// slots are derived from the budget, so that the budget is what binds
	assert.Equal(t, q.maxBytes/minEntryBytes, q.size)
	assert.Len(t, q.elements, int(q.size))
}

func TestFastQueueSlotsFloorSmallBudgets(t *testing.T) {
	// the floor is a budget, not a slot count: budgets under it are raised to
	// it, so both the budget and the ring stay consistent
	for _, kb := range []int{1, 512, minIntakeBufferKB - 1, minIntakeBufferKB} {
		q := newFastQueue(kb)
		assert.GreaterOrEqual(t, q.size, int64(minRingSlots), "budget %dKB", kb)
		assert.Equal(t, q.maxBytes/minEntryBytes, q.size, "budget %dKB", kb)
	}
	assert.GreaterOrEqual(t, newFastQueue(defaultIntakeBufferKB).size, int64(minRingSlots))
}

func TestFastQueueSlotsOutlastBudget(t *testing.T) {
	// however small the entries, the budget runs out before the slots do
	q := newFastQueue(defaultIntakeBufferKB)
	smallest := (&processorInput{}).retainedBytes()
	assert.LessOrEqual(t, q.maxBytes/smallest, q.size)
}

func TestFastQueueDefaultsWhenBudgetNotPositive(t *testing.T) {
	for _, kb := range []int{0, -1} {
		q := newFastQueue(kb)
		assert.Equal(t, int64(defaultIntakeBufferKB)*1024, q.maxBytes)
	}
}
