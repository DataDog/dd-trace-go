// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"sync/atomic"
	"time"
	"unsafe"
)

// defaultIntakeBufferKB is the memory budget, in kibibytes, given to the
// fastQueue when none is configured.
const defaultIntakeBufferKB = 4096

// minEntryBytes is the least an entry can retain: the struct with none of its
// strings or tag slices populated. The byte budget is what bounds the queue,
// so the slot count is derived from this minimum -- budget/minEntryBytes is
// the most entries that can be live at once, which makes the budget, not the
// end of the ring, the limit that binds. Deriving it from a maximum instead
// would under-provision slots and put the ring back in charge.
var minEntryBytes = int64(unsafe.Sizeof(processorInput{}))

// minRingSlots is the smallest ring worth running. Below this the writer laps
// the ring often enough to contend with the reader for the same cache lines,
// and a burst has nowhere to go.
const minRingSlots = 10000

// minIntakeBufferKB is the smallest budget that still buys minRingSlots slots,
// and so the floor on the configuration. internal/config clamps to the same
// number and is what warns the customer about it; the clamp here only covers
// callers that build a Processor directly. The two are kept in step by
// TestDataStreamsIntakeBufferFloorHoldsTenThousandSlots.
var minIntakeBufferKB = int((minRingSlots*minEntryBytes + 1023) / 1024)

// SlotsForKB reports how many ring slots a budget of kb kibibytes buys. It
// exists so that internal/config can assert its own floor against this
// package's sizing.
func SlotsForKB(kb int) int64 { return slotsForBudget(budgetBytes(kb)) }

// budgetBytes converts a configured budget to bytes, holding it at the floor.
func budgetBytes(kb int) int64 {
	if kb < minIntakeBufferKB {
		kb = minIntakeBufferKB
	}
	return int64(kb) * 1024
}

// slotsForBudget sizes the ring for a byte budget.
func slotsForBudget(maxBytes int64) int64 {
	return maxBytes / minEntryBytes
}

// stringHeaderBytes is the size of a string header, charged for each element
// of a retained string slice.
const stringHeaderBytes = int64(unsafe.Sizeof(""))

// retainedBytes is the memory this entry keeps alive: the struct itself plus
// the heap behind its pointers, which is where almost all the variance lives
// (edge and process tags, and the service, topic and group names). The
// processTags backing array is in practice shared by every entry, so charging
// each entry for it overstates the total somewhat; nothing else here is an
// estimate.
func (p *processorInput) retainedBytes() int64 {
	return int64(unsafe.Sizeof(*p)) +
		stringsBytes(p.point.edgeTags) +
		stringsBytes(p.point.processTags) +
		int64(len(p.point.serviceName)) +
		int64(len(p.kafkaOffset.topic)) +
		int64(len(p.kafkaOffset.group))
}

func stringsBytes(s []string) int64 {
	n := int64(len(s)) * stringHeaderBytes
	for _, v := range s {
		n += int64(len(v))
	}
	return n
}

// there are many writers, there is only 1 reader.
// each value will be read at most once.
// reader will stop if it catches up with writer.
// the queue tracks the bytes retained by everything it holds, and once that
// reaches maxBytes writers evict the oldest unread entries to make room, so
// values are dropped oldest-first rather than in an unspecified order.
type fastQueue struct {
	elements []atomic.Pointer[processorInput]
	size     int64
	maxBytes int64
	curBytes atomic.Int64
	writePos atomic.Int64
	readPos  atomic.Int64
}

// newFastQueue creates a fastQueue holding at most bufferKB kibibytes worth of
// entries. A non-positive budget falls back to defaultIntakeBufferKB.
func newFastQueue(bufferKB int) *fastQueue {
	if bufferKB <= 0 {
		bufferKB = defaultIntakeBufferKB
	}
	maxBytes := budgetBytes(bufferKB)
	size := slotsForBudget(maxBytes)
	return &fastQueue{
		elements: make([]atomic.Pointer[processorInput], size),
		size:     size,
		maxBytes: maxBytes,
	}
}

// bytes is the measured footprint of everything currently buffered.
func (q *fastQueue) bytes() int64 {
	return q.curBytes.Load()
}

func (q *fastQueue) push(p *processorInput) (dropped bool) {
	p.sizeBytes = p.retainedBytes()
	nextPos := q.writePos.Add(1)
	p.queuePos = nextPos - 1
	idx := p.queuePos % q.size
	// The ring is sized so the budget binds first, but reclaim runs after the
	// bytes are added, so concurrent writers can briefly hold more than the
	// budget and wrap onto a slot whose entry was never read. That store is
	// what removes the entry -- it leaves through neither pop nor reclaim, so
	// its bytes are discounted here or the total drifts by its size. queuePos
	// identifies it as the previous cycle's occupant; the CAS both claims it
	// (losing to a concurrent pop or eviction, which discount it themselves)
	// and confirms it was still unread.
	displaced := q.elements[idx].Load()
	// bytes are credited before the entry is published, since publishing it
	// makes it visible to a concurrent pop/reclaim/eviction, which would
	// otherwise be able to discount it before its own credit landed.
	q.curBytes.Add(p.sizeBytes)
	q.elements[idx].Store(p)
	if displaced != nil && displaced.queuePos == p.queuePos-q.size &&
		q.readPos.CompareAndSwap(displaced.queuePos, displaced.queuePos+1) {
		q.curBytes.Add(-displaced.sizeBytes)
		dropped = true
	}
	// l is the length of the queue after the element has been added, and before the next element has been read.
	l := nextPos - q.readPos.Load()
	return q.reclaim() || dropped || l > q.size
}

// reclaim drops the oldest unread entries until the queue is back inside its
// byte budget and inside the ring, reporting whether it dropped anything. It
// runs on the writer, since a slow reader is exactly the case that needs it.
func (q *fastQueue) reclaim() (dropped bool) {
	for q.curBytes.Load() > q.maxBytes || q.writePos.Load()-q.readPos.Load() > q.size {
		readPos := q.readPos.Load()
		if q.writePos.Load() <= readPos {
			// nothing buffered to evict; curBytes will settle as the
			// in-flight writes above finish storing their elements.
			return dropped
		}
		loaded := q.elements[readPos%q.size].Load()
		if loaded == nil || loaded.queuePos < readPos {
			// the write for this slot started but hasn't finished; let the
			// next push try again rather than spin here.
			return dropped
		}
		if loaded.queuePos > readPos {
			// readPos fell more than one cycle behind, so the entry actually
			// due here was overwritten before anyone evicted it -- its bytes
			// are already gone and unrecoverable. Catch up to what is
			// physically here without discounting it; it still owes its own
			// discount once readPos reaches its real position.
			q.readPos.CompareAndSwap(readPos, loaded.queuePos)
			continue
		}
		if q.readPos.CompareAndSwap(readPos, readPos+1) {
			q.curBytes.Add(-loaded.sizeBytes)
			dropped = true
		}
	}
	return dropped
}

func (q *fastQueue) pop() *processorInput {
	for {
		readPos := q.readPos.Load()
		if q.writePos.Load() <= readPos {
			return nil
		}
		loaded := q.elements[readPos%q.size].Load()
		if loaded == nil || loaded.queuePos < readPos {
			// the write started, but hasn't finished yet, the element we read
			// is the one from the previous cycle.
			return nil
		}
		if loaded.queuePos > readPos {
			// same as in reclaim: readPos fell more than one cycle behind,
			// so catch it up without treating loaded as the (already lost)
			// entry that was due here.
			q.readPos.CompareAndSwap(readPos, loaded.queuePos)
			continue
		}
		// a writer evicting over-budget entries advances readPos too, so the
		// reader has to claim its position rather than assume it.
		if q.readPos.CompareAndSwap(readPos, readPos+1) {
			q.curBytes.Add(-loaded.sizeBytes)
			return loaded
		}
	}
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
