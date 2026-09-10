// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"sync/atomic"
	"time"
)

// defaultQueueSize is the number of slots in the fastQueue ring buffer used
// when no explicit size is configured.
const defaultQueueSize = 10000

// there are many writers, there is only 1 reader.
// each value will be read at most once.
// reader will stop if it catches up with writer
// if reader is too slow, there is no guarantee in which order values will be dropped.
type fastQueue struct {
	elements []atomic.Pointer[processorInput]
	size     int64
	writePos atomic.Int64
	readPos  atomic.Int64
}

// newFastQueue creates a fastQueue with room for size elements. A
// non-positive size falls back to defaultQueueSize.
func newFastQueue(size int) *fastQueue {
	if size <= 0 {
		size = defaultQueueSize
	}
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
