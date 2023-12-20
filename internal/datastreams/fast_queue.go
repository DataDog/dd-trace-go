// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"sync/atomic"
	"time"
)

const (
	queueSize = 10000
)

// there are many writers, there is only 1 reader.
// each value will be read at most once.
// reader will stop if it catches up with writer
// if reader is too slow, there is no guarantee in which order values will be dropped.
type fastQueue struct {
	elements [queueSize]atomic.Pointer[processorInput]
	writePos atomic.Int64
	readPos  int64
}

func newFastQueue() *fastQueue {
	return &fastQueue{}
}

func (q *fastQueue) push(p *processorInput) {
	ind := q.writePos.Add(1)
	p.queuePos = ind - 1
	q.elements[(ind-1)%queueSize].Store(p)
}

func (q *fastQueue) pop() *processorInput {
	writePos := q.writePos.Load()
	if writePos <= q.readPos {
		return nil
	}
	loaded := q.elements[q.readPos%queueSize].Load()
	if loaded == nil || loaded.queuePos < q.readPos {
		// the write started, but hasn't finished yet, the element we read
		// is the one from the previous cycle.
		return nil
	}
	q.readPos++
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
