package tracer

import (
	"math/rand"
	"sync"
)

const (
	// spanBufferDefaultMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	spanBufferDefaultMaxSize = 10000
	// finishedTracesSize is the initial size of the map used to stores traces
	// considered as finished, and therefore sendable to agent.
	finishedTracesSize = 10
)

// spansBuffer is a threadsafe buffer for spans.
type spansBuffer struct {
	lock           sync.Mutex
	spans          []*Span
	maxSize        int
	finishedTraces map[uint64]struct{} // set of traces considered as finished
	bufferFull     int64               // number of spans we ignored because buffer was full
}

func newSpansBuffer(maxSize int) *spansBuffer {

	// small sanity check on the max size.
	if maxSize <= 0 {
		maxSize = spanBufferDefaultMaxSize
	}

	return &spansBuffer{
		maxSize:        maxSize,
		finishedTraces: make(map[uint64]struct{}, finishedTracesSize),
	}
}

func (sb *spansBuffer) Push(span *Span) {
	sb.lock.Lock()
	if len(sb.spans) < sb.maxSize {
		sb.spans = append(sb.spans, span)
	} else {
		// Here we have a problem, buffer is too small. We shoot a random span
		// and put the most recent span in that place.
		sb.bufferFull++
		idx := rand.Intn(sb.maxSize)
		sb.spans[idx] = span
	}
	// If this was a root or a top-level / local root span, mark the trace as finished.
	// This must really be tested on parent pointer, not on parentID (which can be set
	// manually, typically when doing distributed tracing).
	if span.parent == nil {
		sb.finishedTraces[span.TraceID] = struct{}{}
	}
	sb.lock.Unlock()
}

func (sb *spansBuffer) Pop() []*Span {
	sb.lock.Lock()
	defer sb.lock.Unlock()

	if len(sb.spans) == 0 || len(sb.finishedTraces) == 0 {
		return nil
	}

	j := 0
	k := 0
	var spansToReturn []*Span
	var spansToKeep []*Span

	for _, span := range sb.spans {
		span.RLock()
		if _, ok := sb.finishedTraces[span.TraceID]; ok {
			// return the span, as it belongs to a finished trace
			if spansToReturn == nil {
				spansToReturn = make([]*Span, len(sb.spans))
			}
			spansToReturn[j] = span
			j++
		} else {
			// put the span back in the buffer
			if spansToKeep == nil {
				spansToKeep = make([]*Span, len(sb.spans))
			}
			spansToKeep[k] = span
			k++
		}
		span.RUnlock()
	}

	if spansToKeep == nil {
		sb.spans = nil
	} else {
		sb.spans = spansToKeep[0:k]
	}
	if spansToReturn == nil {
		return nil
	}

	return spansToReturn[0:j]
}

func (sb *spansBuffer) Len() int {
	sb.lock.Lock()
	defer sb.lock.Unlock()
	return len(sb.spans)
}
