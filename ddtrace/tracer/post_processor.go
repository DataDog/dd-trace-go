package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Note: Keeping everything in this file for easy access during development.
// I will likely move the ReadWriteSpan interface to the ddtrace package,
// and the (s *span) *() methods to span.go.

// ReadOnlySpan specifies methods to read from a span.
type readOnlySpan interface {
	// Span name returns the operation of the span.
	SpanName() string
	// etc...
}

// ReadWriteSpan implements the methods of ddtrace.Span (to write to a span)
// and readOnlyspan (to read from a span).
type ReadWriteSpan interface {
	ddtrace.Span
	readOnlySpan
	Remove()
}

// Span name returns the operation name of s.
func (s *span) SpanName() string {
	s.Lock()
	defer s.Unlock()
	// the if statement below is not necessary to
	// prevent a race condition with spans being
	// flushed, as this is a read. I decided
	// to still have this because it should be
	// a user error to call this on a finished span
	// (that is not being processed). I can remove it.
	if s.finished && !s.inProcessor {
		return "span is finished"
	}
	return s.Name
}

// Note: the code to implement this method is not in this PR. I am not sure
// if this should be a part of the API. This would let you remove some spans,
// but not all spans. We would compute the new span slice in finishProcessedSpans
// (for example). If the root span is dropped, we would need to make sure that
// the priority is set on the "new root", finding the "new root" by implementing
// a similar logic to:
// https://github.com/DataDog/datadog-agent/blob/7.38.x/pkg/trace/traceutil/trace.go#L53.
func (s *span) Remove() {
	s.Lock()
	defer s.Unlock()
	// no-op for active spans, or spans that
	// aren't being processed.
	if !s.finished || !s.inProcessor {
		return
	}
	// currently does nothing.
	s.remove = true
}

// processTrace pushes finished spans from a trace to the processor. It
// then computes stats (if client side stats are enabled), and reports whether
// the trace should be dropped.
func processTrace(spans []*span) bool {
	tr, ok := internal.GetGlobalTracer().(*tracer)
	if !ok {
		return true
	}
	shouldKeep := tr.config.postProcessor(newReadWriteSpanSlice(spans))
	finishProcessedSpans(spans)
	return shouldKeep
}

// newReadWriteSpanSlice marks the spans as being in the processor and copies
// the elements of slice spans to the destination slice of type ReadWriteSpan
// to be fed to the processor.
func newReadWriteSpanSlice(spans []*span) []ReadWriteSpan {
	rwSlice := make([]ReadWriteSpan, len(spans))
	for i, span := range spans {
		span.Lock()
		// inProcessor enables methods from ddtrace.Span (and readOnlySpan)
		// to work on finished spans.
		span.inProcessor = true
		span.Unlock()
		rwSlice[i] = span
	}
	return rwSlice
}

// finishProcessedSpans marks the spans as being out of the processor
// and computes stats (if client side stats are enabled).
func finishProcessedSpans(spans []*span) {
	tr, ok := internal.GetGlobalTracer().(*tracer)
	tracerCanComputeStats := ok && tr.config.canComputeStats()
	for _, span := range spans {
		span.Lock()
		span.inProcessor = false
		if tracerCanComputeStats && shouldComputeStats(span) {
			// the agent supports computed stats
			select {
			case tr.stats.In <- newAggregableSpan(span, tr.obfuscator):
				// ok
			default:
				log.Error("Stats channel full, disregarding span.")
			}
		}
		span.Unlock()
	}
}
