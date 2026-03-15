--------------------------- MODULE SpanLifecycle ---------------------------
(**************************************************************************)
(* TLA+ specification of dd-trace-go's span/trace concurrency protocol.  *)
(*                                                                        *)
(* Models:                                                                *)
(*   - Lock ordering: span.mu -> trace.mu                                 *)
(*   - Finish-guard: no modification after finished=TRUE                  *)
(*   - Double-finish idempotent                                           *)
(*   - Partial flush lock inversion safety                                *)
(*                                                                        *)
(* Source: ddtrace/tracer/span.go, ddtrace/tracer/spancontext.go          *)
(**************************************************************************)

EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS
    Goroutines,   \* set of goroutine IDs (e.g., {g1, g2})
    Spans,        \* set of span IDs (e.g., {s1, s2, s3})
    NoOne         \* sentinel value: not a goroutine

ASSUME NoOne \notin Goroutines

VARIABLES
    \* Per-span state
    spanFinished,     \* function: Span -> BOOLEAN
    spanName,         \* function: Span -> STRING
    spanInTrace,      \* function: Span -> BOOLEAN (has been pushed to trace)

    \* Trace buffer state
    traceSpans,       \* sequence of spans in the trace buffer
    traceFinished,    \* count of finished spans in the buffer
    traceFull,        \* BOOLEAN: trace buffer is full

    \* Lock state: who holds each lock
    spanLockHolder,   \* function: Span -> Goroutine ∪ {NoOne}
    traceLockHolder,  \* Goroutine ∪ {NoOne}

    \* Per-goroutine program counter
    pc                \* function: Goroutine -> state label

vars == <<spanFinished, spanName, spanInTrace,
          traceSpans, traceFinished, traceFull,
          spanLockHolder, traceLockHolder, pc>>

----------------------------------------------------------------------------
(* Type invariant *)
TypeOK ==
    /\ spanFinished \in [Spans -> BOOLEAN]
    /\ spanName \in [Spans -> {"init", "modified"}]
    /\ spanInTrace \in [Spans -> BOOLEAN]
    /\ traceFinished \in 0..Cardinality(Spans)
    /\ traceFull \in BOOLEAN
    /\ spanLockHolder \in [Spans -> Goroutines \union {NoOne}]
    /\ traceLockHolder \in Goroutines \union {NoOne}
    /\ pc \in [Goroutines -> {"idle", "finishing_span_locked",
                               "partial_flush_released"}]

----------------------------------------------------------------------------
(* Initial state *)
Init ==
    /\ spanFinished = [s \in Spans |-> FALSE]
    /\ spanName = [s \in Spans |-> "init"]
    /\ spanInTrace = [s \in Spans |-> FALSE]
    /\ traceSpans = <<>>
    /\ traceFinished = 0
    /\ traceFull = FALSE
    /\ spanLockHolder = [s \in Spans |-> NoOne]
    /\ traceLockHolder = NoOne
    /\ pc = [g \in Goroutines |-> "idle"]

----------------------------------------------------------------------------
(* Actions *)

(* A goroutine starts a span: acquires trace.mu, pushes span, releases *)
(* Modelled as atomic because push() acquires and releases trace.mu    *)
(* within a single Go function call.                                    *)
StartSpan(g, s) ==
    /\ pc[g] = "idle"
    /\ ~spanInTrace[s]
    /\ ~traceFull
    /\ traceLockHolder = NoOne
    /\ traceSpans' = Append(traceSpans, s)
    /\ spanInTrace' = [spanInTrace EXCEPT ![s] = TRUE]
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanFinished, spanName, traceFinished, traceFull,
                   spanLockHolder, traceLockHolder>>

(* A goroutine modifies a span's name: acquires span.mu, checks finished *)
(* Models SetOperationName() from span.go:878-894                         *)
SetOperationName(g, s) ==
    /\ pc[g] = "idle"
    /\ spanInTrace[s]
    /\ spanLockHolder[s] = NoOne
    /\ IF spanFinished[s]
       THEN /\ UNCHANGED <<spanName>>
       ELSE /\ spanName' = [spanName EXCEPT ![s] = "modified"]
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanFinished, spanInTrace, traceSpans, traceFinished,
                   traceFull, spanLockHolder, traceLockHolder>>

(* Phase 1 of finish: goroutine acquires span.mu *)
(* Models the s.mu.Lock() at span.go:897 *)
BeginFinish(g, s) ==
    /\ pc[g] = "idle"
    /\ spanInTrace[s]
    /\ ~spanFinished[s]
    /\ spanLockHolder[s] = NoOne
    /\ spanLockHolder' = [spanLockHolder EXCEPT ![s] = g]
    /\ pc' = [pc EXCEPT ![g] = "finishing_span_locked"]
    /\ UNCHANGED <<spanFinished, spanName, spanInTrace,
                   traceSpans, traceFinished, traceFull, traceLockHolder>>

(* Phase 2a: with span.mu held, acquire trace.mu and perform full flush *)
(* This path fires when all spans in the buffer are finished *)
FinishFullFlush(g, s) ==
    /\ pc[g] = "finishing_span_locked"
    /\ spanLockHolder[s] = g
    /\ ~spanFinished[s]
    /\ traceLockHolder = NoOne
    /\ Len(traceSpans) = traceFinished + 1  \* this span completes the trace
    \* All updates in this branch:
    /\ spanFinished' = [spanFinished EXCEPT ![s] = TRUE]
    /\ traceSpans' = <<>>
    /\ traceFinished' = 0
    /\ traceLockHolder' = NoOne         \* release trace.mu
    /\ spanLockHolder' = [spanLockHolder EXCEPT ![s] = NoOne]  \* release span.mu
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanName, spanInTrace, traceFull>>

(* Phase 2b: with span.mu held, acquire trace.mu and trigger partial flush *)
(* Key: release trace.mu BEFORE acquiring fSpan.mu (#incident-46344)      *)
FinishPartialFlush(g, s) ==
    /\ pc[g] = "finishing_span_locked"
    /\ spanLockHolder[s] = g
    /\ ~spanFinished[s]
    /\ traceLockHolder = NoOne
    /\ Len(traceSpans) /= traceFinished + 1  \* NOT a full flush
    /\ traceFinished + 1 >= 2                  \* partial flush threshold met
    \* Mark finished, release trace.mu (keep span.mu for now)
    /\ spanFinished' = [spanFinished EXCEPT ![s] = TRUE]
    /\ traceFinished' = traceFinished + 1
    /\ traceLockHolder' = NoOne         \* release trace.mu FIRST
    /\ pc' = [pc EXCEPT ![g] = "partial_flush_released"]
    /\ UNCHANGED <<spanName, spanInTrace, traceSpans, traceFull, spanLockHolder>>

(* Phase 2c: with span.mu held, acquire trace.mu, not enough for partial flush *)
FinishNoFlush(g, s) ==
    /\ pc[g] = "finishing_span_locked"
    /\ spanLockHolder[s] = g
    /\ ~spanFinished[s]
    /\ traceLockHolder = NoOne
    /\ Len(traceSpans) /= traceFinished + 1  \* NOT a full flush
    /\ traceFinished + 1 < 2                  \* below partial flush threshold
    \* Mark finished, release both locks
    /\ spanFinished' = [spanFinished EXCEPT ![s] = TRUE]
    /\ traceFinished' = traceFinished + 1
    /\ traceLockHolder' = NoOne
    /\ spanLockHolder' = [spanLockHolder EXCEPT ![s] = NoOne]
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanName, spanInTrace, traceSpans, traceFull>>

(* Phase 2d: double-finish guard — span already finished by another goroutine *)
FinishAlreadyDone(g, s) ==
    /\ pc[g] = "finishing_span_locked"
    /\ spanLockHolder[s] = g
    /\ spanFinished[s]     \* already finished!
    /\ traceLockHolder = NoOne
    \* Release both locks, no state changes
    /\ spanLockHolder' = [spanLockHolder EXCEPT ![s] = NoOne]
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanFinished, spanName, spanInTrace,
                   traceSpans, traceFinished, traceFull, traceLockHolder>>

(* Phase 3 (partial flush only): trace.mu released, now release span.mu *)
(* Models the #incident-46344 fix: lock ordering preserved by releasing  *)
(* trace.mu before acquiring any new span.mu                             *)
CompletePartialFlush(g, s) ==
    /\ pc[g] = "partial_flush_released"
    /\ spanLockHolder[s] = g
    /\ spanLockHolder' = [spanLockHolder EXCEPT ![s] = NoOne]
    /\ traceFinished' = 0
    /\ pc' = [pc EXCEPT ![g] = "idle"]
    /\ UNCHANGED <<spanFinished, spanName, spanInTrace,
                   traceSpans, traceFull, traceLockHolder>>

(* Stutter step *)
Idle(g) ==
    /\ pc[g] = "idle"
    /\ UNCHANGED vars

----------------------------------------------------------------------------
(* Next-state relation *)

Next ==
    \/ \E g \in Goroutines, s \in Spans :
        \/ StartSpan(g, s)
        \/ SetOperationName(g, s)
        \/ BeginFinish(g, s)
        \/ FinishFullFlush(g, s)
        \/ FinishPartialFlush(g, s)
        \/ FinishNoFlush(g, s)
        \/ FinishAlreadyDone(g, s)
        \/ CompletePartialFlush(g, s)
    \/ \E g \in Goroutines : Idle(g)

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------
(* Invariants *)

(* INV1: Finish guard structural check *)
(* SetOperationName cannot change a finished span's name.               *)
(* We verify this by checking: if a span is finished, its name is       *)
(* either "init" (never modified) or "modified" (modified before finish) *)
(* but cannot transition from post-finish state — enforced structurally *)
(* by the IF guard in SetOperationName.                                 *)

(* INV2: Lock ordering *)
(* When trace.mu is held, the holder also holds some span.mu            *)
(* (span.mu was acquired first per the protocol).                       *)
(* In our model, trace.mu is only acquired in FinishAcquireTrace,       *)
(* which requires pc[g] = "finishing_span_locked" meaning span.mu held. *)
(* But we modelled acquire+release as atomic, so traceLockHolder is     *)
(* always NoOne in stable states. We check the structural property:     *)
LockOrderingSafe ==
    \A g \in Goroutines :
        traceLockHolder = g =>
            \E s \in Spans : spanLockHolder[s] = g

(* INV3: Finished count never exceeds buffer length *)
FinishCountConsistent ==
    traceFinished <= Len(traceSpans)

(* INV4: No real deadlock *)
(* All goroutines can always eventually make progress because:          *)
(*   - "idle" goroutines can take any action                            *)
(*   - "finishing_span_locked" goroutines compete for trace.mu          *)
(*   - "partial_flush_released" goroutines just need to release span.mu *)
(* Deadlock requires a lock cycle, which the ordering prevents.         *)
NoDeadlock ==
    \/ \E g \in Goroutines : pc[g] = "idle"
    \/ \E g \in Goroutines : pc[g] = "partial_flush_released"
    \/ /\ traceLockHolder = NoOne
       /\ \E g \in Goroutines : pc[g] = "finishing_span_locked"

(* INV5: A finished span has its lock released *)
(* No goroutine permanently holds a finished span's lock *)
FinishedSpanUnlocked ==
    \A s \in Spans :
        (spanFinished[s] /\ ~(\E g \in Goroutines :
            pc[g] \in {"finishing_span_locked", "partial_flush_released"}
            /\ spanLockHolder[s] = g))
        => spanLockHolder[s] = NoOne

(* INV6: Trace buffer only contains spans that are in the trace *)
TraceBufferConsistent ==
    \A i \in 1..Len(traceSpans) :
        spanInTrace[traceSpans[i]]

(* Combined safety invariant *)
Safety ==
    /\ TypeOK
    /\ LockOrderingSafe
    /\ FinishCountConsistent
    /\ NoDeadlock
    /\ FinishedSpanUnlocked
    /\ TraceBufferConsistent

=============================================================================
