package tracer

import (
	"fmt"
	"sync"
)

const (
	// traceBufferDefaultInitSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceBufferDefaultInitSize = 10
	// traceBufferDefaultMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	traceBufferDefaultMaxSize = 10000
)

type traceBuffer struct {
	traceChan chan<- []*Span
	errChan   chan<- error

	spans         []*Span
	finishedSpans int

	initSize int
	maxSize  int

	sync.RWMutex
}

func newTraceBuffer(traceChan chan<- []*Span, errChan chan<- error, initSize, maxSize int) *traceBuffer {
	if initSize <= 0 {
		initSize = traceBufferDefaultInitSize
	}
	if maxSize <= 0 {
		maxSize = traceBufferDefaultMaxSize
	}
	return &traceBuffer{
		traceChan: traceChan,
		errChan:   errChan,
		initSize:  initSize,
		maxSize:   maxSize,
	}
}

func (tb *traceBuffer) Push(span *Span) {
	if tb == nil {
		return
	}

	tb.Lock()
	defer tb.Unlock()

	if len(tb.spans) > 0 {
		// if traceBuffer is full, forget span
		if len(tb.spans) >= tb.maxSize {
			select {
			case tb.errChan <- fmt.Errorf("[TODO:christian] exceed traceBuffer size"):
			default: // if channel is full, drop & ignore error, better do this than stall program
			}
			return
		}
		// if there's a trace ID mismatch, ignore span
		if tb.spans[0].TraceID != span.TraceID {
			select {
			case tb.errChan <- fmt.Errorf("[TODO:christian] trace ID mismatch"):
			default: // if channel is full, drop & ignore error, better do this than stall program
			}
			return
		}
	}

	if tb.spans == nil {
		tb.spans = make([]*Span, 0, tb.initSize)
	}

	tb.spans = append(tb.spans, span)
}

func (tb *traceBuffer) flushable() bool {
	tb.RLock()
	defer tb.RUnlock()

	if len(tb.spans) == 0 {
		return false
	}

	return tb.finishedSpans == len(tb.spans)
}

func (tb *traceBuffer) ack() {
	tb.Lock()
	defer tb.Unlock()

	tb.finishedSpans++
}

func (tb *traceBuffer) doFlush() {
	if !tb.flushable() {
		return
	}

	tb.Lock()
	defer tb.Unlock()

	select {
	case tb.traceChan <- tb.spans:
	default:
		select {
		case tb.errChan <- fmt.Errorf("[TODO:christian] trace buffer full"):
		default: // if channel is full, drop & ignore error, better do this than stall program
		}
	}
	tb.spans = nil
}

func (tb *traceBuffer) Flush() {
	if tb == nil {
		return
	}
	tb.doFlush()
}

func (tb *traceBuffer) AckFinish() {
	if tb == nil {
		return
	}
	tb.ack()
	tb.doFlush()
}

func (tb *traceBuffer) Len() int {
	if tb == nil {
		return 0
	}
	tb.RLock()
	defer tb.RUnlock()
	return len(tb.spans)
}
