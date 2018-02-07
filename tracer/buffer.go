package tracer

import (
	"sync"
)

var (
	// traceStartSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceStartSize = 10
	// traceMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	traceMaxSize = int(1e5)
)

type spanBuffer struct {
	mu       sync.RWMutex
	tracer   *tracer
	trace    []*span
	finished int
}

func newSpanBuffer(t *tracer) *spanBuffer {
	return &spanBuffer{
		tracer: t,
		trace:  make([]*span, 0, traceStartSize),
	}
}

func (tb *spanBuffer) Push(sp *span) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if len(tb.trace) >= traceMaxSize {
		return &errorSpanBufFull{Len: len(tb.trace)}
	}
	tb.trace = append(tb.trace, sp)
	return nil
}

func (tb *spanBuffer) AckFinish() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.finished++

	if len(tb.trace) != tb.finished {
		return
	}
	tb.tracer.pushTrace(tb.trace)
	tb.trace = nil
	tb.finished = 0 // important, because a buffer can be used for several flushes
}
