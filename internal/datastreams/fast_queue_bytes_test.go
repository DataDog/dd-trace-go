// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// varSizeEntry returns entries whose retained size varies widely, so that
// crediting one entry's size to another drifts the total instead of cancelling
// out.
func varSizeEntry(i int) *processorInput {
	return &processorInput{point: statsPoint{
		hash:        uint64(i),
		edgeTags:    []string{strings.Repeat("x", 1+i%997)},
		serviceName: strings.Repeat("s", i%311),
	}}
}

// An entry can leave the queue three ways -- read by the consumer, evicted for
// being over budget, or overwritten when the ring wraps -- and all three have
// to discount it, so an emptied queue always measures zero.
func TestFastQueueBytesReturnToZero(t *testing.T) {
	for _, tc := range []struct {
		name            string
		bufferKB        int
		pushes          int
		unboundedBudget bool
	}{
		// budget never reached: every entry leaves through pop
		{name: "drained", bufferKB: defaultIntakeBufferKB, pushes: 5000},
		// budget reached constantly: most entries leave through reclaim
		{name: "over-budget", bufferKB: 64, pushes: 20000},
		// budget lifted after construction so it cannot bind, leaving the
		// ring to wrap and entries to leave by being overwritten
		{name: "ring-wrap", bufferKB: 64, pushes: 20000, unboundedBudget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFastQueue(tc.bufferKB)
			if tc.unboundedBudget {
				q.maxBytes = math.MaxInt64
			}
			for i := range tc.pushes {
				q.push(varSizeEntry(i))
			}
			assert.LessOrEqual(t, q.bytes(), q.maxBytes)
			for q.pop() != nil {
			}
			assert.Zero(t, q.bytes())
			assert.Equal(t, q.writePos.Load(), q.readPos.Load())
		})
	}
}

func TestFastQueueBytesConcurrent(t *testing.T) {
	const writers, perWriter = 8, 50000
	q := newFastQueue(256)

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				q.pop()
			}
		}
	}()

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	<-readerDone

	for q.pop() != nil {
	}
	assert.Zero(t, q.bytes())
}

// TestFastQueueConcurrentBytesNeverNegative runs writers and a reader at the
// same time, with a sampler racing both, and checks that curBytes -- which
// every removal path (pop, reclaim, and the overwrite-on-wrap in push) has to
// discount exactly once -- never dips below zero. A double discount would
// show up here before it ever affected the budget check.
func TestFastQueueConcurrentBytesNeverNegative(t *testing.T) {
	const writers, perWriter = 16, 20000
	q := newFastQueue(256)

	stop := make(chan struct{})
	var minObserved atomic.Int64

	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if b := q.bytes(); b < minObserved.Load() {
					minObserved.Store(b)
				}
			}
		}
	})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				q.pop()
			}
		}
	})

	var writersWG sync.WaitGroup
	for w := range writers {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	writersWG.Wait()
	close(stop)
	wg.Wait()

	assert.GreaterOrEqual(t, minObserved.Load(), int64(0))

	for q.pop() != nil {
	}
	assert.Zero(t, q.bytes())
}

// TestFastQueueConcurrentByteBudgetRespected pushes from many writers at once
// against a tight budget with no reader draining it, so reclaim -- which now
// runs on whichever writer's push triggers it, concurrently with every other
// writer's own reclaim call -- carries the entire job of keeping the queue
// inside its budget.
func TestFastQueueConcurrentByteBudgetRespected(t *testing.T) {
	const writers, perWriter = 16, 20000
	q := newFastQueue(256)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	wg.Wait()

	// reclaim runs synchronously inside push, so once every writer has
	// returned the queue is already back inside its budget.
	assert.LessOrEqual(t, q.bytes(), q.maxBytes)

	for q.pop() != nil {
	}
	assert.Zero(t, q.bytes())
}

// TestFastQueueConcurrentNoDuplicatePops runs many writers and a reader
// concurrently and checks that no value is ever returned by pop() twice. pop
// and reclaim both remove entries by racing a CAS on readPos, so a bug there
// would show up as either a duplicate or a value that ends up further ahead
// than readPos should allow.
func TestFastQueueConcurrentNoDuplicatePops(t *testing.T) {
	const writers, perWriter = 16, 20000
	const total = writers * perWriter
	q := newFastQueue(4096) // generous budget: this test is about races, not eviction

	seen := make([]atomic.Bool, total)
	var duplicates atomic.Int64

	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if p := q.pop(); p != nil {
					if seen[p.point.hash].Swap(true) {
						duplicates.Add(1)
					}
				}
			}
		}
	})

	var writersWG sync.WaitGroup
	for w := range writers {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	writersWG.Wait()

	// drain whatever the reader goroutine hasn't gotten to yet before
	// stopping it, so a slow reader doesn't cost us pending entries.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if q.readPos.Load() == q.writePos.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	readerWG.Wait()

	for p := q.pop(); p != nil; p = q.pop() {
		if seen[p.point.hash].Swap(true) {
			duplicates.Add(1)
		}
	}

	assert.Zero(t, duplicates.Load())
}

// TestFastQueueConcurrentDropAccounting checks that every pushed entry is
// accounted for exactly once: readPos advances by one for every removal,
// whether that removal is a pop or an eviction, so once the queue is fully
// drained readPos must equal writePos must equal the number of entries
// pushed -- neither more (an entry counted as removed twice) nor less (an
// entry never counted as removed at all).
func TestFastQueueConcurrentDropAccounting(t *testing.T) {
	const writers, perWriter = 16, 20000
	const total = writers * perWriter
	// A tight budget with no reader forces reclaim to drop most entries, so
	// this exercises the eviction accounting under writer/writer contention.
	q := newFastQueue(64)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	wg.Wait()

	popped := 0
	for q.pop() != nil {
		popped++
	}

	assert.Equal(t, int64(total), q.writePos.Load())
	assert.Equal(t, int64(total), q.readPos.Load())
	assert.Zero(t, q.bytes())
	assert.Greater(t, total-popped, 0, "a 64KB budget should have forced evictions")
}

// TestFastQueueConcurrentMultipleReclaimers forces every push to run reclaim
// -- the budget holds only a handful of entries -- from many goroutines at
// once, with no reader, so reclaim's own CAS loop is what has to keep readPos
// and curBytes consistent under writer/writer contention alone.
func TestFastQueueConcurrentMultipleReclaimers(t *testing.T) {
	const writers, perWriter = 32, 5000
	const total = writers * perWriter
	q := newFastQueue(defaultIntakeBufferKB)
	q.maxBytes = 4 * (&processorInput{}).retainedBytes()

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				q.push(varSizeEntry(w*perWriter + i))
			}
		}(w)
	}
	wg.Wait()

	assert.LessOrEqual(t, q.bytes(), q.maxBytes)
	assert.LessOrEqual(t, q.readPos.Load(), q.writePos.Load())

	for q.pop() != nil {
	}
	assert.Zero(t, q.bytes())
	assert.Equal(t, int64(total), q.writePos.Load())
	assert.Equal(t, q.writePos.Load(), q.readPos.Load())
}
