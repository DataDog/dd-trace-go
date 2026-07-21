# Formal Verification of dd-trace-go Concurrency Patterns

This directory contains [Specula](https://github.com/specula-org/Specula)-based formal verification for the concurrency invariants in dd-trace-go's tracer.

## What Gets Verified

### Phase 1: Span Lifecycle Model

Verifies the core span/trace concurrency protocol:

| Invariant | Source | What It Means |
|-----------|--------|---------------|
| **No modification after finish** | `span.go:889` | Once `finished=true`, mutable fields cannot change |
| **Lock ordering** | `spancontext.go:617` | `span.mu` is always acquired before `trace.mu` |
| **Finish idempotent** | `spancontext.go:634` | Double-finish is a no-op, not a race |
| **Partial flush safety** | `spancontext.go:714-731` | Lock inversion during partial flush is deadlock-free |
| **Sampling atomicity** | `spancontext.go:434` | `samplingDecision` transitions are atomic |

### Phase 2: GLS Push/Pop Model

Verifies the orchestrion goroutine-local storage protocol:

| Invariant | Source | What It Means |
|-----------|--------|---------------|
| **Push/Pop pairing** | `context.go:19`, `span.go:875` | Every `Push(ActiveSpanKey)` has a `Pop(ActiveSpanKey)` |
| **No leak on finish** | `context_stack.go:51-74` | Finished spans do not remain on the GLS stack |
| **LIFO ordering** | `context_stack.go:42-48` | Nested spans pop in reverse order |

## Quick Start

```bash
# Install Specula and check prerequisites
make setup

# Run Phase 1 (span lifecycle)
make run-phase1

# Run Phase 2 (GLS push/pop)
make run-phase2

# Run both phases
make run-all

# Dry-run (see what would execute)
make run-all DRY_RUN=--dry-run
```

## Prerequisites

- Python 3.8+
- Java 11+ (for TLC model checker)
- `ANTHROPIC_API_KEY` environment variable
- ~5-10 minutes and ~$5-10 in API costs

## Directory Structure

```
formal-verification/
├── README.md                 # This file
├── Makefile                  # Convenience targets
├── docs/                     # Detailed documentation
│   ├── 01-background.md      # Why formal verification for dd-trace-go
│   ├── 02-specula-setup.md   # Installation guide
│   ├── 03-span-lifecycle-model.md  # Phase 1 model explanation
│   ├── 04-gls-push-pop-model.md    # Phase 2 model explanation
│   └── 05-interpreting-results.md  # Reading TLC output
├── source/                   # Extracted Go concurrency protocols
│   ├── go.mod                # Standalone module for compilation
│   ├── spanlifecycle/
│   │   └── span_lifecycle.go # Span/trace lock ordering and finish-guard
│   └── glscontext/
│       └── gls_context.go    # GLS push/pop pairing
├── config/                   # Specula configuration
│   ├── span_lifecycle_config.yaml
│   └── gls_context_config.yaml
├── scripts/                  # Pipeline scripts
│   ├── setup.sh              # Install Specula + deps
│   ├── run-span-lifecycle.sh # Phase 1 pipeline
│   ├── run-gls-context.sh    # Phase 2 pipeline
│   └── run-all.sh            # Run all phases
└── specs/                    # Generated TLA+ (tracked after review)
```

## How Specula Works

Specula synthesizes TLA+ formal specifications from Go source code through a 4-step pipeline:

1. **Step 1 — LLM Translation**: Claude translates Go concurrency patterns into a TLA+ draft, using RAG-based syntax correction to avoid common TLA+ pitfalls
2. **Step 2 — Control Flow Analysis**: Transforms the procedural TLA+ draft into a declarative specification amenable to model checking
3. **Step 3 — TLC Model Checking**: The TLC model checker exhaustively explores all possible interleavings, with automated fix attempts (up to 5) for specification issues
4. **Step 4 — Trace Validation**: Optionally validates generated TLA+ traces against instrumented Go tests

Steps 1-3 provide the core value. Step 4 is optional and requires test instrumentation.

## Cost Estimate

Based on Specula's etcd Raft baseline ($3.54 for a more complex model):

- Phase 1 (span lifecycle): ~$3-5
- Phase 2 (GLS push/pop): ~$2-4
- **Total: ~$5-10**

## Relationship to Existing CI

This formal verification complements (does not replace) existing safety mechanisms:

| Mechanism | Coverage | Scope |
|-----------|----------|-------|
| `-race` flag | Dynamic race detection | Interleavings exercised by tests |
| `checklocks` (disabled) | Static lock annotation | Annotated fields only |
| Runtime assertions | Crash on violation | Production paths only |
| **Specula (this)** | **All interleavings** | **Modelled invariants** |
