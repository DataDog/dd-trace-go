package tracer

import (
	"fmt"
	"sync"
)

const (
	// traceBufferDefaultMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	traceBufferDefaultMaxSize = 10000
)

type traceBuffer struct {
	// spans is a traceBuffer containing all the spans for this trace.
	// The reason we don't use a channel here, is we regularly need
	// to walk the array to find out if it's done or not.
	spans   []*Span
	maxSize int

	traceChan chan<- []*Span
	errChan   chan<- error

	sync.RWMutex
}

func newTraceBuffer(traceChan chan<- []*Span, errChan chan<- error, maxSize int) *traceBuffer {
	if maxSize <= 0 {
		maxSize = traceBufferDefaultMaxSize
	}
	return &traceBuffer{
		traceChan: traceChan,
		errChan:   errChan,
		maxSize:   maxSize,
	}
}

func (tb *traceBuffer) doPush(span *Span) {
	tb.Lock()
	defer tb.Unlock()

	// if traceBuffer is full, forget span
	if len(tb.spans) >= tb.maxSize {
		select {
		case tb.errChan <- fmt.Errorf("[TODO:christian] exceed traceBuffer size"):
		default: // if channel is full, drop & ignore error, better do this than stall program
		}
		return
	}
	// if there's a trace ID mismatch, ignore span
	if len(tb.spans) > 0 && tb.spans[0].TraceID != span.TraceID {
		select {
		case tb.errChan <- fmt.Errorf("[TODO:christian] trace ID mismatch"):
		default: // if channel is full, drop & ignore error, better do this than stall program
		}
		return
	}

	tb.spans = append(tb.spans, span)
}

func (tb *traceBuffer) Push(span *Span) {
	if tb == nil {
		return
	}
	tb.doPush(span)
}

func (tb *traceBuffer) flushable() bool {
	tb.RLock()
	defer tb.RUnlock()

	if len(tb.spans) == 0 {
		return false
	}

	for _, span := range tb.spans {
		span.RLock()
		finished := span.finished
		span.RUnlock()

		// A note about performance: it can seem a performance killer
		// to range over all spans each time we finish a span (flush should
		// be called whenever a span is finished) but... by design the
		// first span (index 0) is the root span, and most of the time
		// it's the last one being finished. So in 99% of cases, this
		// is going to return false at the first iteration.
		if !finished {
			return false
		}
	}

	return true
}

func (tb *traceBuffer) doFlush() {
	if !tb.flushable() {
		return
	}

	tb.Lock()
	defer tb.Unlock()

	tb.traceChan <- tb.spans
	tb.spans = nil
}

func (tb *traceBuffer) Flush() {
	if tb == nil {
		return
	}
	tb.doFlush()
}
