------------------------------ MODULE GLSContext ------------------------------
(**************************************************************************)
(* TLA+ specification of dd-trace-go's GLS push/pop protocol.            *)
(*                                                                        *)
(* Models:                                                                *)
(*   - Push/Pop pairing: every push has a corresponding pop               *)
(*   - Stack depth correctness                                            *)
(*   - No leak on finish                                                  *)
(*   - LIFO ordering                                                      *)
(*   - Nested span support                                                *)
(*                                                                        *)
(* Source: internal/orchestrion/context_stack.go,                          *)
(*         internal/orchestrion/context.go,                                *)
(*         ddtrace/tracer/context.go:18-19,                               *)
(*         ddtrace/tracer/span.go:875                                     *)
(**************************************************************************)

EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS
    Goroutines,   \* set of goroutine IDs (e.g., {g1, g2})
    SpanIDs,      \* set of span IDs (e.g., {1, 2, 3})
    NoOne         \* sentinel value: not a goroutine

ASSUME NoOne \notin Goroutines

VARIABLES
    \* GLS state: per-goroutine stack of active spans
    glsStack,       \* function: Goroutine -> Seq(SpanID)

    \* Span lifecycle state
    spanStarted,    \* function: SpanID -> BOOLEAN
    spanFinished,   \* function: SpanID -> BOOLEAN
    spanGoroutine,  \* function: SpanID -> Goroutine ∪ {NoOne}

    \* Per-goroutine program counter
    pc              \* function: Goroutine -> state label

vars == <<glsStack, spanStarted, spanFinished, spanGoroutine, pc>>

----------------------------------------------------------------------------
(* Type invariant *)
TypeOK ==
    /\ \A g \in Goroutines : glsStack[g] \in Seq(SpanIDs)
    /\ spanStarted \in [SpanIDs -> BOOLEAN]
    /\ spanFinished \in [SpanIDs -> BOOLEAN]
    /\ spanGoroutine \in [SpanIDs -> Goroutines \union {NoOne}]
    /\ pc \in [Goroutines -> {"idle", "span_active", "finishing"}]

(* Helper: elements in a sequence as a set *)
SeqToSet(seq) == {seq[i] : i \in 1..Len(seq)}

----------------------------------------------------------------------------
(* Initial state *)
Init ==
    /\ glsStack = [g \in Goroutines |-> <<>>]
    /\ spanStarted = [s \in SpanIDs |-> FALSE]
    /\ spanFinished = [s \in SpanIDs |-> FALSE]
    /\ spanGoroutine = [s \in SpanIDs |-> NoOne]
    /\ pc = [g \in Goroutines |-> "idle"]

----------------------------------------------------------------------------
(* Actions *)

(* StartSpan: push span onto GLS stack *)
(* Models: ContextWithSpan -> CtxWithValue -> getDDContextStack().Push() *)
StartSpan(g, s) ==
    /\ pc[g] \in {"idle", "span_active"}  \* can nest spans
    /\ ~spanStarted[s]                     \* span not yet used
    /\ spanStarted' = [spanStarted EXCEPT ![s] = TRUE]
    /\ spanGoroutine' = [spanGoroutine EXCEPT ![s] = g]
    /\ glsStack' = [glsStack EXCEPT ![g] = Append(@, s)]
    /\ pc' = [pc EXCEPT ![g] = "span_active"]
    /\ UNCHANGED <<spanFinished>>

(* FinishSpan: mark finished and pop from GLS stack *)
(* Models: Span.Finish() -> finish() + GLSPopValue() *)
FinishSpan(g, s) ==
    /\ pc[g] = "span_active"
    /\ spanStarted[s]
    /\ ~spanFinished[s]
    /\ spanGoroutine[s] = g
    \* LIFO: can only finish the top-of-stack span
    /\ Len(glsStack[g]) > 0
    /\ glsStack[g][Len(glsStack[g])] = s
    \* Pop from GLS stack
    /\ glsStack' = [glsStack EXCEPT ![g] = SubSeq(@, 1, Len(@) - 1)]
    /\ spanFinished' = [spanFinished EXCEPT ![s] = TRUE]
    \* If stack is now empty, go idle; otherwise stay active
    /\ pc' = [pc EXCEPT ![g] = IF Len(glsStack[g]) - 1 = 0
                                 THEN "idle"
                                 ELSE "span_active"]
    /\ UNCHANGED <<spanStarted, spanGoroutine>>

(* Peek: read top of stack without modification *)
(* Models: SpanFromContext -> WrapContext -> glsContext.Value -> Peek *)
Peek(g) ==
    /\ pc[g] = "span_active"
    /\ Len(glsStack[g]) > 0
    \* Read-only operation, no state change
    /\ UNCHANGED vars

(* Idle: goroutine does nothing *)
Idle(g) ==
    /\ pc[g] = "idle"
    /\ UNCHANGED vars

----------------------------------------------------------------------------
(* Next-state relation *)

Next ==
    \/ \E g \in Goroutines, s \in SpanIDs :
        \/ StartSpan(g, s)
        \/ FinishSpan(g, s)
    \/ \E g \in Goroutines :
        \/ Peek(g)
        \/ Idle(g)

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------
(* Invariants *)

(* INV1: Push/Pop Pairing *)
(* When all spans on a goroutine are finished, the GLS stack is empty *)
PushPopPairing ==
    \A g \in Goroutines :
        (\A s \in SpanIDs : spanGoroutine[s] = g => spanFinished[s])
        => Len(glsStack[g]) = 0

(* INV2: Stack Depth Correctness *)
(* The GLS stack depth equals the number of started-but-not-finished *)
(* spans on this goroutine *)
StackDepthCorrect ==
    \A g \in Goroutines :
        Len(glsStack[g]) = Cardinality(
            {s \in SpanIDs : spanGoroutine[s] = g
                          /\ spanStarted[s]
                          /\ ~spanFinished[s]})

(* INV3: No Leak On Finish *)
(* A finished span is NOT on any GLS stack *)
NoLeakOnFinish ==
    \A s \in SpanIDs :
        spanFinished[s] =>
            ~(\E g \in Goroutines : s \in SeqToSet(glsStack[g]))

(* INV4: Peek Returns Latest *)
(* If the stack is non-empty, the top element is a started, non-finished span *)
PeekReturnsValid ==
    \A g \in Goroutines :
        Len(glsStack[g]) > 0 =>
            LET top == glsStack[g][Len(glsStack[g])]
            IN /\ spanStarted[top]
               /\ ~spanFinished[top]
               /\ spanGoroutine[top] = g

(* INV5: Stack Contains Only Active Spans *)
(* Every span on the GLS stack is started but not finished *)
StackContainsOnlyActive ==
    \A g \in Goroutines :
        \A i \in 1..Len(glsStack[g]) :
            LET s == glsStack[g][i]
            IN /\ spanStarted[s]
               /\ ~spanFinished[s]

(* INV6: Goroutine Isolation *)
(* A span on goroutine g's stack is not on any other goroutine's stack *)
GoroutineIsolation ==
    \A g1, g2 \in Goroutines :
        g1 /= g2 =>
            SeqToSet(glsStack[g1]) \intersect SeqToSet(glsStack[g2]) = {}

(* INV7: No duplicate spans in any stack *)
NoDuplicatesInStack ==
    \A g \in Goroutines :
        Cardinality(SeqToSet(glsStack[g])) = Len(glsStack[g])

(* Combined safety invariant *)
Safety ==
    /\ TypeOK
    /\ PushPopPairing
    /\ StackDepthCorrect
    /\ NoLeakOnFinish
    /\ PeekReturnsValid
    /\ StackContainsOnlyActive
    /\ GoroutineIsolation
    /\ NoDuplicatesInStack

=============================================================================
