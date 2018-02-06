package tracer

import (
	"sync"
)

const (
	// spanBufferDefaultInitSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	spanBufferDefaultInitSize = 10
	// spanBufferDefaultMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	spanBufferDefaultMaxSize = 1e5
)

type spanBuffer struct {
	tracer *Tracer

	spans         []*span
	finishedSpans int

	initSize int
	maxSize  int

	sync.RWMutex
}

func newSpanBuffer(t *Tracer, initSize, maxSize int) *spanBuffer {
	if initSize <= 0 {
		initSize = spanBufferDefaultInitSize
	}
	if maxSize <= 0 {
		maxSize = spanBufferDefaultMaxSize
	}
	return &spanBuffer{
		tracer:   t,
		initSize: initSize,
		maxSize:  maxSize,
	}
}

func (tb *spanBuffer) Push(sp *span) {
	if tb == nil {
		return
	}

	tb.Lock()
	defer tb.Unlock()

	if len(tb.spans) > 0 {
		// if spanBuffer is full, forget span
		if len(tb.spans) >= tb.maxSize {
			tb.tracer.pushErr(&errorSpanBufFull{Len: len(tb.spans)})
			return
		}
		// if there's a trace ID mismatch, ignore span
		if tb.spans[0].TraceID != sp.TraceID {
			tb.tracer.pushErr(&errorTraceIDMismatch{Expected: tb.spans[0].TraceID, Actual: sp.TraceID})
			return
		}
	}

	if tb.spans == nil {
		tb.spans = make([]*span, 0, tb.initSize)
	}

	tb.spans = append(tb.spans, sp)
}

func (tb *spanBuffer) flushable() bool {
	tb.RLock()
	defer tb.RUnlock()

	if len(tb.spans) == 0 {
		return false
	}

	return tb.finishedSpans == len(tb.spans)
}

func (tb *spanBuffer) ack() {
	tb.Lock()
	defer tb.Unlock()

	tb.finishedSpans++
}

func (tb *spanBuffer) doFlush() {
	if !tb.flushable() {
		return
	}

	tb.Lock()
	defer tb.Unlock()

	tb.tracer.pushTrace(tb.spans)
	tb.spans = nil
	tb.finishedSpans = 0 // important, because a buffer can be used for several flushes
}

func (tb *spanBuffer) Flush() {
	if tb == nil {
		return
	}
	tb.doFlush()
}

func (tb *spanBuffer) AckFinish() {
	if tb == nil {
		return
	}
	tb.ack()
	tb.doFlush()
}

func (tb *spanBuffer) Len() int {
	if tb == nil {
		return 0
	}
	tb.RLock()
	defer tb.RUnlock()
	return len(tb.spans)
}
