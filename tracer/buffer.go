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
		finishedTraces: make(map[uint64]struct{}),
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

// Pop gets all the spans within the span buffer.
// WARNING: this is deprecated, use PopTraces instead, unless you really know
// what you are doing, as Pop returns spans from possibly partially finished
// traces, and thus yields wrong data later in the pipeline.
func (sb *spansBuffer) Pop() []*Span {
	sb.lock.Lock()
	defer sb.lock.Unlock()

	if len(sb.spans) == 0 {
		return nil
	}

	spans := sb.spans
	sb.spans = nil

	return spans
}

func (sb *spansBuffer) PopTraces() [][]*Span {
	sb.lock.Lock()
	defer sb.lock.Unlock()

	if len(sb.spans) == 0 || len(sb.finishedTraces) == 0 {
		return nil
	}

	traceBuffer := make(map[uint64][]*Span, len(sb.finishedTraces))
	for traceID := range sb.finishedTraces {
		// pre-allocate some memory, with 200% of the average length
		// so that we don't allocate too much but still avoid re-allocating too often
		traceBuffer[traceID] = make([]*Span, 0, 2*len(sb.spans)/len(sb.finishedTraces))
	}

	i := 0
	for _, span := range sb.spans {
		// Note: we access span.TraceID without locking here, this is fine because
		// this span has already been finished and recorded, so obviously no other
		// thread should still be modifying it at this point.
		traceID := span.TraceID
		if _, ok := sb.finishedTraces[traceID]; ok {
			traceBuffer[traceID] = append(traceBuffer[traceID], span)
		} else {
			// put the span back in the buffer
			sb.spans[i] = span
			i++
		}
	}

	// truncating current buffer to its useful size, no need to alloc anything
	sb.spans = sb.spans[0:i]

	traces := make([][]*Span, len(traceBuffer))
	i = 0
	for _, trace := range traceBuffer {
		traces[i] = trace
		i++
	}

	// Reset the finished traces map, and pre-allocate it to about 75% of its
	// previous size. This way, it is going to shrink after some time, but also
	// we don't re-allocate memory over and over when adding members.
	sb.finishedTraces = make(map[uint64]struct{}, ((len(sb.finishedTraces)*3)/4)+1)

	return traces
}

func (sb *spansBuffer) Len() int {
	sb.lock.Lock()
	defer sb.lock.Unlock()
	return len(sb.spans)
}
