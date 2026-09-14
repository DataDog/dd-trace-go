// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// there are many writers, there is only 1 reader.
// each value will be read at most once.
// reader will stop if it catches up with writer
// if reader is too slow, there is no guarantee in which order values will be dropped.
type fastQueue struct {
	elements []atomic.Pointer[processorInput]
	// size is the number of slots in elements, fixed at construction and only read
	// afterwards, so it needs no synchronization. Indexing stays a modulo rather than a
	// power-of-two mask because the floor is exactly minRingSlots, which is not a power
	// of two. The compiler can no longer strength-reduce the division that a constant
	// size allowed, which measures at +0.08 ns/op uncontended and nothing at all with
	// writers contending, where the atomic add dominates.
	size     int64
	writePos atomic.Int64
	readPos  atomic.Int64
}

func newFastQueue() *fastQueue {
	budget, found := memoryBudget()
	slots := ringSlots(budget, found)
	// The resolved size is otherwise invisible from outside the process, which is
	// precisely when it is worth knowing.
	if found {
		log.Debug("datastreams: input ring sized to %d slots (~%d bytes at capacity) from a %d byte process memory budget", slots, slots*ringBytesPerSlot, budget)
	} else {
		log.Debug("datastreams: input ring sized to %d slots (~%d bytes at capacity); no process memory budget found", slots, slots*ringBytesPerSlot)
	}
	return newFastQueueSize(slots)
}

func newFastQueueSize(size int) *fastQueue {
	return &fastQueue{
		elements: make([]atomic.Pointer[processorInput], size),
		size:     int64(size),
	}
}

func (q *fastQueue) push(p *processorInput) (dropped bool) {
	nextPos := q.writePos.Add(1)
	// l is the length of the queue after the element has been added, and before the next element has been read.
	l := nextPos - q.readPos.Load()
	p.queuePos = nextPos - 1
	q.elements[(nextPos-1)%q.size].Store(p)
	return l > q.size
}

func (q *fastQueue) pop() *processorInput {
	writePos := q.writePos.Load()
	readPos := q.readPos.Load()
	if writePos <= readPos {
		return nil
	}
	loaded := q.elements[readPos%q.size].Load()
	if loaded == nil || loaded.queuePos < readPos {
		// the write started, but hasn't finished yet, the element we read
		// is the one from the previous cycle.
		return nil
	}
	q.readPos.Add(1)
	return loaded
}

func (q *fastQueue) poll(timeout time.Duration) *processorInput {
	deadline := time.Now().Add(timeout)
	for {
		if p := q.pop(); p != nil {
			return p
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
