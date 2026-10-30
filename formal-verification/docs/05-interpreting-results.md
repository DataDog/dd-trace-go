# Interpreting Specula/TLC Results

## TLC Output Overview

After running the Specula pipeline, the TLC model checker produces output that tells you whether each invariant holds for all reachable states. This guide explains how to read that output.

## Successful Verification

When all invariants pass:

```
Model checking completed. No error has been found.
  Estimates of the probability that TLC did not check all reachable states
  because two distinct states had the same fingerprint:
  calculated (optimistic):  val: 1.2E-15
  based on the initial state: val: 5.8E-16
14523 states generated, 4201 distinct states found, 0 states left on queue.
The depth of the complete state graph search is 27.
```

Key indicators:
- **"No error has been found"**: All invariants hold
- **States generated/distinct**: Size of the state space explored
- **Depth**: Longest sequence of actions checked
- **Fingerprint probability**: Negligible chance of hash collision (safe to ignore if < 1E-10)

## Invariant Violation

When TLC finds a counterexample:

```
Error: Invariant NoModificationAfterFinish is violated.
Error: The behavior up to this point is:
State 1: <Initial predicate>
/\ spans = <<>>
/\ finished = [s1 |-> FALSE, s2 |-> FALSE]
/\ locks = [g1 |-> {}, g2 |-> {}]

State 2: <StartSpan(g1, s1)>
/\ spans = <<s1>>
/\ finished = [s1 |-> FALSE, s2 |-> FALSE]
/\ locks = [g1 |-> {}, g2 |-> {}]

State 3: <FinishSpan(g1, s1)>
/\ spans = <<>>
/\ finished = [s1 |-> TRUE, s2 |-> FALSE]
/\ locks = [g1 |-> {}, g2 |-> {}]

State 4: <SetOperationName(g2, s1, "new_name")>
/\ spans = <<>>
/\ finished = [s1 |-> TRUE, s2 |-> FALSE]
/\ locks = [g1 |-> {}, g2 |-> {}}
```

This trace shows the **exact sequence of actions** that leads to the violation. Read it bottom-up to understand how the invariant was broken.

**Interpretation**: In State 4, goroutine g2 modifies span s1's operation name after s1 was already finished in State 3. This would be a real bug if the model accurately reflects the code.

## Deadlock Detection

```
Error: Deadlock reached.
Error: The behavior up to this point is:
...
State 5: <AcquireLock(g1, trace_mu)>
/\ locks = [g1 |-> {span_mu, trace_mu}, g2 |-> {trace_mu}]

State 6: Deadlock
```

TLC reports deadlock when no action is enabled in a state. Check the `locks` variable to see which goroutines hold which locks and identify the cycle.

## Common Issues

### State Space Explosion

```
TLC ran out of memory after generating 50,000,000 states.
```

**Cause**: Too many goroutines or spans in the model constants.

**Fix**: Reduce `NUM_GOROUTINES` and `NUM_SPANS` in the config YAML:

```yaml
model:
  constants:
    NUM_GOROUTINES: 2  # start with 2
    NUM_SPANS: 2       # start with 2
```

Most concurrency bugs are exposable with 2 goroutines and 2-3 spans. If verification passes at this scale, increasing the numbers provides higher confidence but rarely finds additional bugs.

### Spurious Deadlocks

TLC may report deadlock in states that represent normal termination (all spans finished, no more work to do). These are **not real deadlocks**.

**Fix**: Add a termination condition to the specification:

```tla
Termination == \A s \in Spans: finished[s] = TRUE
Spec == Init /\ [][Next]_vars /\ ([]<>Termination \/ <>Termination)
```

Or filter the TLC output to ignore deadlocks where all spans are finished.

### CFA Failure (Step 2)

If Specula's Control Flow Analysis fails to transform the procedural TLA+ into declarative form:

```
CFA Error: Unable to transform action FinishSpan
```

**Workaround**: Skip Step 2 and proceed to Step 3 with the Step 1 output:

```bash
./scripts/run-span-lifecycle.sh --from 3
```

You may need to manually adjust the TLA+ specification. See Specula's documentation for CFA limitations.

### Model Doesn't Match Code

If TLC reports no violations but you know a bug exists (or vice versa), the model may not accurately reflect the code:

1. Compare the generated TLA+ spec with the Go source
2. Check that lock acquisition order matches
3. Verify that the `finished` flag semantics are preserved
4. Ensure the partial flush path is correctly modelled

The `specs/` directory contains the generated specifications. Cross-reference with `source/span_lifecycle.go` and `source/gls_context.go`.

## Reading TLA+ Specifications

Key TLA+ syntax for reading generated specs:

| TLA+ | Meaning |
|------|---------|
| `/\` | AND |
| `\/` | OR |
| `~` | NOT |
| `=>` | IMPLIES |
| `[]` | Always (in all states) |
| `<>` | Eventually (in some state) |
| `[]<>` | Infinitely often |
| `\A x \in S:` | For all x in set S |
| `\E x \in S:` | There exists x in set S |
| `x' = expr` | Next-state value of x |
| `UNCHANGED x` | x does not change in this step |
| `Len(seq)` | Length of sequence |
| `Head(seq)` | First element |
| `Tail(seq)` | All but first element |

## Next Steps After Verification

1. **All invariants pass**: Document the result and commit the verified spec to `specs/`
2. **Invariant violated**: Analyze the counterexample trace, determine if it's a real bug or a model issue
3. **State explosion**: Reduce model size or add symmetry optimizations
4. **CFA failure**: Try manual TLA+ adjustments or skip to Step 3
