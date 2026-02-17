// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package spanlifecycle extracts the concurrency protocol from dd-trace-go's
// span and trace types. This file is the input to Specula's Go→TLA+ translation.
//
// It preserves lock acquisition order, finish-guard semantics, and partial-flush
// lock inversion — the three invariants we want to formally verify.
//
// Non-concurrency concerns (pprof, serialization, telemetry, obfuscation) are
// replaced with stubs so Specula's control-flow analysis stays focused on the
// interleaving space.
package spanlifecycle

import "sync"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Span represents a computation. Fields relevant to the concurrency protocol
// are retained; everything else is stubbed out.
type Span struct {
	mu       sync.RWMutex
	name     string
	finished bool // true once the span has been submitted; can only be read/modified if the trace is locked
	context  *SpanContext
}

// SpanContext links a span to its containing trace.
type SpanContext struct {
	trace *trace
}

// samplingDecision indicates whether to send the trace to the agent.
type samplingDecision uint32

const (
	decisionNone samplingDecision = iota
	decisionDrop
	decisionKeep
)

// trace contains shared context information about a trace. The fields below
// are the subset involved in the concurrency protocol.
type trace struct {
	mu               sync.RWMutex
	spans            []*Span
	finished         int
	full             bool
	priority         *float64
	locked           bool
	samplingDecision samplingDecision // +checkatomic
	root             *Span
}

// ---------------------------------------------------------------------------
// Span methods — finish-guard pattern
// ---------------------------------------------------------------------------

// SetOperationName demonstrates the finish-guard: once finished is true,
// mutations are rejected.
//
// We don't lock spans when flushing, so we could have a data race when
// modifying a span as it's being flushed. This protects us against that
// race, since spans are marked `finished` before we flush them.
func (s *Span) SetOperationName(operationName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return
	}
	s.name = operationName
}

// Finish closes this span. It delegates to finish() then performs
// a GLS pop (modelled separately in gls_context.go).
func (s *Span) Finish() {
	if s == nil {
		return
	}
	s.finish()
	// orchestrion.GLSPopValue(ActiveSpanKey) — modelled in gls_context.go
}

// finish acquires span.mu, marks the span finished, and calls
// context.finish() which acquires trace.mu.
//
// Lock ordering: span.mu → trace.mu.
func (s *Span) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		// Double-finish guard: idempotent.
		return
	}

	// --- Stub: duration computation, serialization, telemetry elided ---

	// Call context.finish() which handles trace-level bookkeeping.
	// Lock ordering: span.mu → trace.mu. The caller (this function) holds s.mu.
	s.context.finish()
}

// ---------------------------------------------------------------------------
// SpanContext.finish — delegates to trace.finishedOneLocked
// ---------------------------------------------------------------------------

func (sc *SpanContext) finish() {
	sc.trace.finishedOneLocked(sc.trace.root) // simplified: always the root for model checking
}

// ---------------------------------------------------------------------------
// trace methods
// ---------------------------------------------------------------------------

// push adds a span to the trace buffer.
func (t *trace) push(sp *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.full {
		return
	}

	// Capacity check (stubbed constant).
	const traceMaxSize = 100
	if len(t.spans) >= traceMaxSize {
		t.full = true
		t.spans = nil
		return
	}
	t.spans = append(t.spans, sp)
}

// finishedOneLocked acknowledges that another span in the trace has finished.
//
// Lock ordering: span.mu → trace.mu. The caller holds s.mu.
// This function acquires t.mu.
//
// Key protocol points:
//  1. Double-finish guard (s.finished check)
//  2. Full flush when all spans are done
//  3. Partial flush with lock inversion (#incident-46344)
func (t *trace) finishedOneLocked(s *Span) {
	t.mu.Lock()

	if t.full {
		t.mu.Unlock()
		return
	}

	if s.finished {
		// Double-finish guard at trace level.
		t.mu.Unlock()
		return
	}
	s.finished = true
	t.finished++

	// --- Full flush: all spans finished ---
	if len(t.spans) == t.finished {
		spans := t.spans
		t.spans = nil
		t.finished = 0
		t.mu.Unlock()
		_ = spans // submit(spans) — stubbed
		return
	}

	// --- Partial flush path ---
	// Check if partial flush is triggered.
	const partialFlushMinSpans = 2
	doPartialFlush := t.finished >= partialFlushMinSpans
	if !doPartialFlush {
		t.mu.Unlock()
		return
	}

	finishedSpans := make([]*Span, 0, t.finished)
	leftoverSpans := make([]*Span, 0, len(t.spans)-t.finished)
	for _, s2 := range t.spans {
		if s2.finished {
			finishedSpans = append(finishedSpans, s2)
		} else {
			leftoverSpans = append(leftoverSpans, s2)
		}
	}

	fSpan := finishedSpans[0]
	currentSpanIsFirstInChunk := s == fSpan

	// #incident-46344 — lock inversion for safety:
	// Release trace.mu BEFORE acquiring fSpan.mu to preserve ordering.
	t.spans = leftoverSpans
	t.finished = 0
	t.mu.Unlock()

	// Set sampling priority and trace-level tags on first span in chunk.
	// If fSpan == s, lock is already held by caller; otherwise acquire it.
	if !currentSpanIsFirstInChunk {
		fSpan.mu.Lock()
		defer fSpan.mu.Unlock()
	}
	// --- Stub: setMetricLocked, setTraceTagsLocked ---

	_ = finishedSpans // submitChunk(finishedSpans) — stubbed
}

// ---------------------------------------------------------------------------
// Invariants to verify (expressed as TLA+ temporal properties)
// ---------------------------------------------------------------------------
//
// INV1: NoModificationAfterFinish
//   ∀ s ∈ Spans: s.finished = TRUE ⟹ □(s.name = s.name')
//   A finished span's mutable fields never change.
//
// INV2: LockOrdering
//   ∀ goroutine g: if g holds trace.mu, then g does NOT already hold span.mu
//     unless it is on the partial flush path where trace.mu was released first.
//
// INV3: FinishIdempotent
//   ∀ s ∈ Spans: calling finish(s) twice leaves state unchanged after the first call.
//
// INV4: PartialFlushSafety
//   The lock inversion in the partial flush path does not introduce deadlock
//   because trace.mu is always released before fSpan.mu is acquired.
//
// INV5: SamplingDecisionAtomicity
//   samplingDecision transitions are atomic (modelled as a single TLA+ step).
